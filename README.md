# Integração do Score de Uso

## Objetivo
Capturar os dados de Score de Uso dos clientes de dentro do projeto Mitra para a base do Analytics de Sucesso do Cliente.

## Bases capturadas
- URL da API: https://analytics2.mitrasheet.com:4434/rest/v0

(A definir)

## Tech stack
- Golang
- net/http
- goroutines

## Estrutura do projeto

```
src/                         Pacote main — wrapper fino que dispara integration.Run()
  main.go
internal/integration/        Biblioteca com toda a lógica da integração
  config.go                  Config + carregamento/validação de variáveis de ambiente
  contracts.go               Contratos de dados Mitra/Sankhya e conversões
  integration.go             Requisições HTTP, pipelines, batching e orquestração
tests/                       Testes caixa-preta (package integration_test)
  integration_test.go
build/                       Binário compilado (gerado)
```

> Os testes ficam em `tests/` como caixa-preta: importam `internal/integration`
> e exercitam apenas a API pública. Por isso a lógica vive numa biblioteca
> importável, e não no `package main`.

## Configuração

As credenciais são lidas de variáveis de ambiente (ou de um arquivo `.env` no
diretório de trabalho, opcional — em produção elas vêm direto do ambiente):

| Variável        | Descrição                          |
| --------------- | ---------------------------------- |
| `BASE_URL`      | URL base da API                    |
| `MITRA_TOKEN`   | Token de autenticação do Mitra     |
| `SANKHYA_TOKEN` | Token de autenticação do Sankhya   |

Todas são obrigatórias; a inicialização falha cedo se alguma estiver ausente.

## Comandos

```bash
# Rodar a integração (a partir da raiz, onde o .env está acessível)
go run ./src

# Compilar o binário para build/
go build -o build/integracao-score-uso ./src

# Rodar todos os testes
go test ./...

# Modo rápido (pula o teste de batching, que tem sleep real entre lotes)
go test ./... -short

# Análise estática
go vet ./...
```

> O `.env` é carregado a partir do diretório de trabalho em runtime. Ao executar
> o binário em `build/`, rode-o de onde o `.env` esteja acessível.

## Pontos importantes
- Esse projeto usa recursos multithreading para máxima performance em produção e diminuição de tempo de execução no GitHub Actions.
- Cada cubo roda em sua própria goroutine; os erros são agregados ao final, então uma pipeline com falha não derruba as demais.
- Adicionar um novo cubo é uma única linha no registro `pipelines` em `internal/integration/integration.go`.
