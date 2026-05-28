package integration_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"joseferreirasankhya/integracao-score-uso/internal/integration"
)

// === Infra de teste ===

// fakeServer simula o endpoint Mitra/Sankhya: responde GET com getBody e
// registra cada payload recebido via POST. postStatus permite forçar um erro.
type fakeServer struct {
	getBody    []byte
	postStatus int // 0 => 200 OK

	mu     sync.Mutex
	posted [][]any
}

func (f *fakeServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			_, _ = w.Write(f.getBody)
		case http.MethodPost:
			body, _ := io.ReadAll(r.Body)
			var batch []any
			_ = json.Unmarshal(body, &batch)

			f.mu.Lock()
			f.posted = append(f.posted, batch)
			f.mu.Unlock()

			if f.postStatus != 0 {
				w.WriteHeader(f.postStatus)
			}
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	})
}

// totalPosted devolve o total de registros recebidos somando todos os lotes.
func (f *fakeServer) totalPosted() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	total := 0
	for _, b := range f.posted {
		total += len(b)
	}
	return total
}

// startFake sobe um servidor de teste e devolve o Config apontado para ele.
func startFake(t *testing.T, fake *fakeServer) integration.Config {
	t.Helper()
	srv := httptest.NewServer(fake.handler())
	t.Cleanup(srv.Close)
	return integration.Config{BaseURL: srv.URL, MitraToken: "tok", SankhyaToken: "tok"}
}

// mitraResponse serializa items no formato { "Content": [...] } esperado por GetMitra.
func mitraResponse(t *testing.T, items any) []byte {
	t.Helper()
	data, err := json.Marshal(map[string]any{"Content": items})
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	return data
}

// === DoRequest ===

func TestDoRequestErroDeStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("falhou"))
	}))
	t.Cleanup(srv.Close)

	_, err := integration.DoRequest(http.MethodGet, srv.URL, "tok", nil)
	if err == nil {
		t.Fatal("esperava erro para status 500, veio nil")
	}
	if !strings.Contains(err.Error(), "500") {
		t.Errorf("erro deveria mencionar o status 500: %v", err)
	}
}

// === GetMitra ===

func TestGetMitra(t *testing.T) {
	fake := &fakeServer{getBody: mitraResponse(t, []integration.ProcessoMitra{
		{ID: "1", Descricao: "A"},
		{ID: "2", Descricao: "B"},
	})}
	cfg := startFake(t, fake)

	items, err := integration.GetMitra[integration.ProcessoMitra](cfg, "Processo")
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, quero 2", len(items))
	}
	if items[0].ID != "1" || items[0].Descricao != "A" {
		t.Errorf("item[0] decodificado errado: %+v", items[0])
	}
}

// === PostSankhya ===

func TestPostSankhyaEmLoteUnico(t *testing.T) {
	fake := &fakeServer{}
	cfg := startFake(t, fake)

	items := []integration.ProcessoMitra{{ID: "1"}, {ID: "2"}, {ID: "3"}}
	result, err := integration.PostSankhya(cfg, "Processo", items)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if result.Records != 3 || result.Batches != 1 {
		t.Errorf("result = %+v, quero Records=3 Batches=1", result)
	}
	if got := fake.totalPosted(); got != 3 {
		t.Errorf("registros enviados = %d, quero 3", got)
	}
}

func TestPostSankhyaDivideEmLotes(t *testing.T) {
	// SankhyaBatchSize+1 registros => 2 lotes. Incorre num sleep real de
	// batchesTimeInterval entre os lotes; pulamos em modo -short.
	if testing.Short() {
		t.Skip("pula teste de batching para evitar o sleep entre lotes")
	}

	items := make([]integration.ProcessoMitra, integration.SankhyaBatchSize+1)
	for i := range items {
		items[i] = integration.ProcessoMitra{ID: strconv.Itoa(i)}
	}

	fake := &fakeServer{}
	cfg := startFake(t, fake)

	result, err := integration.PostSankhya(cfg, "Processo", items)
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if result.Batches != 2 {
		t.Errorf("Batches = %d, quero 2", result.Batches)
	}
	if got := fake.totalPosted(); got != integration.SankhyaBatchSize+1 {
		t.Errorf("registros enviados = %d, quero %d", got, integration.SankhyaBatchSize+1)
	}
}

func TestPostSankhyaPropagaErro(t *testing.T) {
	fake := &fakeServer{postStatus: http.StatusBadGateway}
	cfg := startFake(t, fake)

	_, err := integration.PostSankhya(cfg, "Processo", []integration.ProcessoMitra{{ID: "1"}})
	if err == nil {
		t.Fatal("esperava erro quando o POST falha, veio nil")
	}
}

// === RunCube (filtro) ===

func TestRunCubeDescartaInvalidos(t *testing.T) {
	fake := &fakeServer{getBody: mitraResponse(t, []integration.MapeamentoTelasMitra{
		{ID: "TELA_1"}, // válido
		{ID: "tela'1"}, // inválido (aspas) => filtrado por keep
		{ID: "-999"},   // descartável => descartado
		{ID: "MOD.2"},  // válido
	})}
	cfg := startFake(t, fake)

	pipeline := integration.RunCube[integration.MapeamentoTelasMitra](integration.ValidResourceID.MatchString)
	result, err := pipeline(cfg, "Mapeamento")
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if result.Records != 2 {
		t.Errorf("Records = %d, quero 2 (TELA_1 e MOD.2)", result.Records)
	}
	if got := fake.totalPosted(); got != 2 {
		t.Errorf("registros enviados = %d, quero 2", got)
	}
}

// === RunPipeline ===

func TestRunPipelinePontaAPonta(t *testing.T) {
	fake := &fakeServer{getBody: mitraResponse(t, []integration.ProcessoMitra{
		{ID: "1", Descricao: "A"},
		{ID: "-999", Descricao: "ignorar"},
		{ID: "2", Descricao: "B"},
	})}
	cfg := startFake(t, fake)

	result, err := integration.RunPipeline(cfg, "Processo")
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if result.Records != 2 {
		t.Errorf("Records = %d, quero 2 (o -999 deve ser descartado)", result.Records)
	}
}

func TestRunPipelineCuboDesconhecido(t *testing.T) {
	cfg := integration.Config{BaseURL: "http://exemplo", MitraToken: "t", SankhyaToken: "t"}
	_, err := integration.RunPipeline(cfg, "Inexistente")
	if err == nil {
		t.Fatal("esperava erro para cubo desconhecido, veio nil")
	}
}

// === LoadConfig ===

func TestLoadConfigVariavelAusente(t *testing.T) {
	// Define duas e deixa BASE_URL vazia. godotenv.Load não sobrescreve vars já
	// presentes no ambiente, então o .env do repo não interfere aqui.
	t.Setenv("BASE_URL", "")
	t.Setenv("MITRA_TOKEN", "tok")
	t.Setenv("SANKHYA_TOKEN", "tok")

	_, err := integration.LoadConfig()
	if err == nil {
		t.Fatal("esperava erro com BASE_URL ausente, veio nil")
	}
	if !strings.Contains(err.Error(), "BASE_URL") {
		t.Errorf("erro deveria citar BASE_URL: %v", err)
	}
}

func TestLoadConfigSucesso(t *testing.T) {
	t.Setenv("BASE_URL", "http://exemplo")
	t.Setenv("MITRA_TOKEN", "m")
	t.Setenv("SANKHYA_TOKEN", "s")

	cfg, err := integration.LoadConfig()
	if err != nil {
		t.Fatalf("erro inesperado: %v", err)
	}
	if cfg.BaseURL != "http://exemplo" || cfg.MitraToken != "m" || cfg.SankhyaToken != "s" {
		t.Errorf("config carregada errada: %+v", cfg)
	}
}

// === CubeResult ===

func TestCubeResultString(t *testing.T) {
	r := integration.CubeResult{Cube: "Processo", Records: 5, Batches: 1, BatchSize: 200}
	got := r.String()
	for _, want := range []string{"Processo", "5 registro", "1 lote", "200"} {
		if !strings.Contains(got, want) {
			t.Errorf("String() = %q, faltou %q", got, want)
		}
	}
}
