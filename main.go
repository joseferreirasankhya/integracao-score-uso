package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"sync"
	"time"

	"github.com/joho/godotenv"
)

// Sankhya rejects resourceIDs com caracteres que ele trata como metacaracteres
// de escape/template (aspas simples, ${...}, <%...%>, etc.). Aplicado apenas
// ao cubo "Mapeamento Telas - Novo AE", cujos IDs são identificadores técnicos.
var validResourceID = regexp.MustCompile(`^[A-Za-z0-9._\-]+$`)

const sankhyaBatchSize = 200

var BaseURL string = getEnv("BASE_URL")

func getEnv(variable string) string {
	err := godotenv.Load()
	if err != nil {
		log.Fatalln("Erro ao carregar variáveis!")
	}

	return os.Getenv(variable)
}

func getMitra[T any](cube string) []T {
	mitraToken := getEnv("MITRA_TOKEN")
	url := BaseURL + "/" + cube

	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		log.Fatalln("Erro ao criar requisição:", err)
	}
	req.Header.Set("Authorization", "Bearer "+mitraToken)

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		log.Fatalln("Erro ao executar requisição:", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalln("Erro ao ler resposta:", err)
	}

	var response Response[T]
	if err := json.Unmarshal(body, &response); err != nil {
		log.Fatalln("Erro ao decodificar resposta:", err)
	}
	return response.Content
}

func postSankhya[M SankhyaConvertible](cube string, items []M) string {
	sankhyaToken := getEnv("SANKHYA_TOKEN")
	url := BaseURL + "/" + cube

	payload := make([]any, 0, len(items))
	for _, item := range items {
		payload = append(payload, item.ToSankhya())
	}

	batches := 0
	for start := 0; start < len(payload); start += sankhyaBatchSize {
		if start > 0 {
			time.Sleep(time.Second) // máximo de um lote por segundo
		}

		end := start + sankhyaBatchSize
		if end > len(payload) {
			end = len(payload)
		}

		postSankhyaBatch(url, sankhyaToken, payload[start:end])
		batches++
		log.Printf("%s: lote %d enviado (%d/%d)", cube, batches, end, len(payload))
	}

	return fmt.Sprintf("Sankhya '%s': %d registro(s) salvo(s) em %d lote(s) de até %d", cube, len(payload), batches, sankhyaBatchSize)
}

func postSankhyaBatch(url, token string, lote []any) {
	data, err := json.Marshal(lote)
	if err != nil {
		log.Fatalln("Erro ao serializar payload:", err)
	}

	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		log.Fatalln("Erro ao criar requisição:", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		log.Fatalln("Erro ao executar requisição:", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Fatalln("Erro ao ler resposta:", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		log.Fatalln("Erro ao salvar no Sankhya:", resp.Status, string(body))
	}
}

// runCube fetches a cube from Mitra and posts it to Sankhya. It is generic over
// the Mitra type, so each registry entry binds a concrete type at compile time.
func runCube[T SankhyaConvertible](cube string) string {
	items := getMitra[T](cube)
	filteredData := items[:0]

	for _, it := range items {
		if it.GetID() == "-999" {
			continue
		}

		filteredData = append(filteredData, it)
	}

	return postSankhya(cube, filteredData)
}

// pipelines maps a cube name to its fully-typed pipeline. Adding a new cube is a
// single line here.
var pipelines = map[string]func(cube string) string{
	"Processo":                   runCube[ProcessoMitra],
	"Subprocesso":                runCube[SubprocessoMitra],
	"Mapeamento Telas - Novo AE": runMapeamentoTelas,
}

func runMapeamentoTelas(cube string) string {
	items := getMitra[MapeamentoTelasMitra](cube)
	filtered := items[:0]
	skipped := 0
	for _, it := range items {
		id := it.GetID()
		if id == "-999" {
			continue
		}
		if !validResourceID.MatchString(id) {
			skipped++
			log.Printf("%s: ID inválido ignorado: %q", cube, id)
			continue
		}
		filtered = append(filtered, it)
	}
	if skipped > 0 {
		log.Printf("%s: %d registro(s) ignorado(s) por ID inválido", cube, skipped)
	}
	return postSankhya(cube, filtered)
}

func runPipeline(cube string) string {
	pipeline, ok := pipelines[cube]
	if !ok {
		log.Fatalln("Cube desconhecido:", cube)
	}
	return pipeline(cube)
}

func main() {
	var wg sync.WaitGroup

	cubes := [3]string{
		"Processo",
		"Subprocesso",
		"Mapeamento Telas - Novo AE",
	}

	start := time.Now()

	for _, cube := range cubes {
		wg.Go(func() {
			fmt.Println(runPipeline(cube))
		})
	}

	wg.Wait()

	elapsed := time.Since(start)
	fmt.Println("Todos os processamentos concluídos com sucesso!")
	fmt.Printf("Tempo total de execução: %f\n", elapsed.Seconds())
}
