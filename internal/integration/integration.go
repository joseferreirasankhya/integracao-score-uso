package integration

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"sort"
	"sync"
	"time"
)

// === Variáveis, configurações ===

// ValidResourceID: o Sankhya rejeita resourceIDs com caracteres que ele trata
// como metacaracteres de escape/template (aspas simples, ${...}, <%...%>, etc.).
// Aplicado apenas ao cubo "Mapeamento Telas - Novo AE", cujos IDs são
// identificadores técnicos.
var ValidResourceID = regexp.MustCompile(`^[A-Za-z0-9._\-]+$`)

const (
	// SankhyaBatchSize é o número máximo de registros por lote enviado ao Sankhya.
	SankhyaBatchSize = 100

	discardableID = "-999"

	// maxConcurrentBatches limita quantos lotes ficam em voo simultaneamente.
	// Junto com o rate limiter, desacopla a proteção contra rate-limit da
	// latência de rede: os POSTs se sobrepõem em vez de ficarem em fila.
	maxConcurrentBatches = 8

	// sankhyaRateLimit é o teto de requisições por segundo enviadas ao Sankhya.
	// Substitui o antigo sleep fixo de 500ms entre lotes; sankhyaRateBurst é
	// quantas podem sair de imediato. Ajuste ao limite real do endpoint.
	sankhyaRateLimit = 8
	sankhyaRateBurst = 8
)

// httpClient reaproveita conexões entre lotes. Os limites por host são elevados
// até a concorrência de envio — o transport default limita
// MaxIdleConnsPerHost a 2, o que serializaria POSTs concorrentes.
var httpClient = newHTTPClient()

func newHTTPClient() *http.Client {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.MaxIdleConns = 100
	t.MaxIdleConnsPerHost = maxConcurrentBatches
	t.MaxConnsPerHost = maxConcurrentBatches
	return &http.Client{Timeout: 30 * time.Second, Transport: t}
}

// rateLimiter é um token bucket simples: começa com `burst` tokens e repõe um a
// cada intervalo até o teto de `ratePerSec`/s. wait bloqueia até haver token;
// close encerra a goroutine de reposição.
type rateLimiter struct {
	tokens chan struct{}
	stop   chan struct{}
}

func newRateLimiter(ratePerSec, burst int) *rateLimiter {
	rl := &rateLimiter{
		tokens: make(chan struct{}, burst),
		stop:   make(chan struct{}),
	}
	for range burst {
		rl.tokens <- struct{}{}
	}
	go func() {
		ticker := time.NewTicker(time.Second / time.Duration(ratePerSec))
		defer ticker.Stop()
		for {
			select {
			case <-rl.stop:
				return
			case <-ticker.C:
				select {
				case rl.tokens <- struct{}{}: // repõe um token
				default: // bucket cheio: descarta
				}
			}
		}
	}()
	return rl
}

func (rl *rateLimiter) wait()  { <-rl.tokens }
func (rl *rateLimiter) close() { close(rl.stop) }

// === Funções ===

func DoRequest(method, url, token string, body []byte) ([]byte, error) {
	var payload io.Reader
	if body != nil {
		payload = bytes.NewReader(body)
	}

	req, err := http.NewRequest(method, url, payload)
	if err != nil {
		return nil, fmt.Errorf("criando requisição %s %s: %w", method, url, err)
	}

	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executando requisição %s %s: %w", method, url, err)
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("lendo resposta de %s: %w", url, err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("requisição %s %s retornou %d: %s", method, url, resp.StatusCode, data)
	}

	return data, nil
}

func GetMitra[T any](cfg Config, cube string) ([]T, error) {
	url := cfg.BaseURL + "/" + cube
	data, err := DoRequest(http.MethodGet, url, cfg.MitraToken, nil)
	if err != nil {
		return nil, err
	}

	var response Response[T]
	if err := json.Unmarshal(data, &response); err != nil {
		return nil, fmt.Errorf("decodificando resposta do cubo %q: %w", cube, err)
	}

	return response.Content, nil
}

// CubeResult resume o que foi enviado por uma pipeline. Mantém apenas os dados;
// a apresentação fica isolada no método String.
type CubeResult struct {
	Cube      string
	Records   int
	Batches   int
	BatchSize int
	Skipped   int // registros descartados pelo filtro keep (não pelo discardableID)
}

func (r CubeResult) String() string {
	s := fmt.Sprintf("Sankhya '%s': %d registro(s) salvo(s) em %d lote(s) de até %d", r.Cube, r.Records, r.Batches, r.BatchSize)
	if r.Skipped > 0 {
		s += fmt.Sprintf(" (%d ignorado(s) por filtro)", r.Skipped)
	}
	return s
}

func PostSankhya[M SankhyaConvertible](cfg Config, cube string, items []M) (CubeResult, error) {
	url := cfg.BaseURL + "/" + cube

	payload := make([]any, 0, len(items))
	for _, item := range items {
		payload = append(payload, item.ToSankhya())
	}

	// Fatia o payload em lotes. Como o cubo é um upsert por ID, a ordem de
	// envio não importa, então os lotes podem ir em paralelo.
	var batches [][]any
	for start := 0; start < len(payload); start += SankhyaBatchSize {
		end := min(start+SankhyaBatchSize, len(payload))
		batches = append(batches, payload[start:end])
	}

	progress.total(cube, len(batches))

	limiter := newRateLimiter(sankhyaRateLimit, sankhyaRateBurst)
	defer limiter.close()

	sem := make(chan struct{}, maxConcurrentBatches)
	var wg sync.WaitGroup
	errs := make([]error, len(batches))

	for i, batch := range batches {
		wg.Add(1)
		sem <- struct{}{} // bloqueia se já há maxConcurrentBatches em voo
		go func() {
			defer wg.Done()
			defer func() { <-sem }()

			limiter.wait() // respeita o teto de req/s antes de disparar
			if err := postSankhyaBatch(cfg, url, batch); err != nil {
				errs[i] = fmt.Errorf("enviando lote %d do cubo %q: %w", i+1, cube, err)
				return
			}
			progress.batchDone(cube)
		}()
	}
	wg.Wait()

	if err := errors.Join(errs...); err != nil {
		return CubeResult{}, err
	}

	return CubeResult{
		Cube:      cube,
		Records:   len(payload),
		Batches:   len(batches),
		BatchSize: SankhyaBatchSize,
	}, nil
}

func postSankhyaBatch(cfg Config, url string, batch []any) error {
	data, err := json.Marshal(batch)
	if err != nil {
		return fmt.Errorf("serializando payload: %w", err)
	}

	_, err = DoRequest(http.MethodPost, url, cfg.SankhyaToken, data)
	return err
}

// RunCube monta uma pipeline que busca um cubo no Mitra e o envia ao Sankhya. É
// genérica sobre o tipo Mitra, então cada entrada do registro vincula um tipo
// concreto em tempo de compilação. O predicado keep opcional decide quais IDs
// passam (além do descarte fixo do discardableID); passe nil para aceitar todos.
func RunCube[T SankhyaConvertible](keep func(id string) bool) func(cfg Config, cube string) (CubeResult, error) {
	return func(cfg Config, cube string) (CubeResult, error) {
		progress.fetching(cube)

		items, err := GetMitra[T](cfg, cube)
		if err != nil {
			return CubeResult{}, err
		}

		filtered := items[:0]
		skipped := 0
		for _, it := range items {
			id := it.GetID()
			if id == discardableID {
				continue
			}
			if keep != nil && !keep(id) {
				skipped++
				continue
			}
			filtered = append(filtered, it)
		}

		result, err := PostSankhya(cfg, cube, filtered)
		result.Skipped = skipped
		return result, err
	}
}

// pipelines mapeia o nome de um cubo para sua pipeline totalmente tipada.
// Adicionar um novo cubo é uma única linha aqui.
var pipelines = map[string]func(cfg Config, cube string) (CubeResult, error){
	"Processo":                   RunCube[ProcessoMitra](nil),
	"Subprocesso":                RunCube[SubprocessoMitra](nil),
	"Mapeamento Telas - Novo AE": RunCube[MapeamentoTelasMitra](ValidResourceID.MatchString),
}

func RunPipeline(cfg Config, cube string) (CubeResult, error) {
	pipeline, ok := pipelines[cube]
	if !ok {
		return CubeResult{}, fmt.Errorf("cubo desconhecido: %q", cube)
	}
	return pipeline(cfg, cube)
}

// Run carrega a configuração e executa todas as pipelines registradas em
// paralelo, renderizando o andamento de cada uma. É o ponto de entrada que o
// main apenas dispara.
//
// A apresentação se adapta ao ambiente: num terminal interativo mostra um
// painel com uma barra de progresso por cubo, atualizando ao vivo; com a saída
// redirecionada (produção, CI, arquivo) degrada para linhas de log limpas, sem
// ANSI nem redesenho.
func Run() error {
	u := newUI(os.Stdout)
	u.header()

	u.step("Carregando configuração")
	cfg, err := LoadConfig()
	if err != nil {
		u.stepFail()
		return fmt.Errorf("carregando configuração: %w", err)
	}
	u.stepOK()

	// Fonte única dos cubos: as chaves do registro pipelines. Ordenamos para
	// que a apresentação seja determinística, já que a execução é concorrente.
	cubes := make([]string, 0, len(pipelines))
	for cube := range pipelines {
		cubes = append(cubes, cube)
	}
	sort.Strings(cubes)

	// Instala o sink de progresso conforme o ambiente. A partir daqui as
	// pipelines reportam cada fase/lote através dele.
	if u.tty {
		progress = newMPBReporter(cubes, u.w, u.color)
	} else {
		progress = newPlainReporter(u.w)
	}

	start := time.Now()

	var wg sync.WaitGroup
	results := make([]CubeResult, len(cubes))
	errs := make([]error, len(cubes))

	for i, cube := range cubes {
		wg.Go(func() {
			result, err := RunPipeline(cfg, cube)
			results[i], errs[i] = result, err
			if err != nil {
				progress.failed(cube, err)
			} else {
				progress.succeeded(cube, result)
			}
		})
	}

	wg.Wait()
	progress.wait() // garante que as barras terminem de desenhar antes do resumo

	return u.summary(cubes, results, errs, time.Since(start))
}
