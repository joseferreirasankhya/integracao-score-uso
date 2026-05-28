package integration

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vbauerster/mpb/v8"
	"github.com/vbauerster/mpb/v8/decor"
)

// progress é o sink de apresentação da execução. Fica como noopReporter (sem
// efeito) por padrão — é o que os testes caixa-preta enxergam ao chamar a API
// pública direto. O Run() o substitui por uma implementação concreta conforme o
// ambiente (barras ao vivo num terminal, log limpo quando a saída é redirecionada).
//
// É escrito uma única vez no início do Run(), antes das goroutines dos cubos, e
// só lido depois disso; cada implementação concreta é, ela própria, segura para
// uso concorrente. Segue o mesmo espírito do httpClient deste pacote: infra
// compartilhada de processo, não estado de negócio (esse continua passado por Config).
var progress reporter = noopReporter{}

// reporter recebe os eventos de progresso de cada cubo. Os métodos são chamados
// concorrentemente (um cubo por goroutine) e identificados pelo nome do cubo.
type reporter interface {
	fetching(cube string)            // começou a buscar dados no Mitra
	total(cube string, batches int)  // nº de lotes a enviar (conhecido após o slice)
	batchDone(cube string)           // um lote concluído
	succeeded(cube string, r CubeResult)
	failed(cube string, err error)
	wait() // bloqueia até todo o rendering terminar
}

// === noopReporter (default / testes) ===

type noopReporter struct{}

func (noopReporter) fetching(string)             {}
func (noopReporter) total(string, int)           {}
func (noopReporter) batchDone(string)            {}
func (noopReporter) succeeded(string, CubeResult) {}
func (noopReporter) failed(string, error)        {}
func (noopReporter) wait()                        {}

// === plainReporter (saída não-interativa: produção, CI, arquivo) ===

// plainReporter emite linhas limpas e atômicas, sem ANSI nem redesenho — seguro
// para logs capturados. Os ticks por lote são silenciados de propósito para não
// inundar o log; o que importa é o início e o fim de cada cubo.
type plainReporter struct {
	mu sync.Mutex
	w  io.Writer
}

func newPlainReporter(w io.Writer) *plainReporter { return &plainReporter{w: w} }

func (r *plainReporter) logf(format string, a ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	fmt.Fprintf(r.w, format+"\n", a...)
}

func (r *plainReporter) fetching(cube string)     { r.logf("→ %s: buscando dados…", cube) }
func (r *plainReporter) total(cube string, n int) { r.logf("→ %s: enviando %d lote(s)…", cube, n) }
func (r *plainReporter) batchDone(string)         {}

// succeeded é silencioso de propósito: o recap por cubo fica a cargo do resumo
// final (ui.summary), evitando duplicar a linha de conclusão no log.
func (r *plainReporter) succeeded(string, CubeResult) {}

// failed, ao contrário, fala alto na hora — um erro no meio de uma execução
// longa merece aparecer no stream, não só no resumo.
func (r *plainReporter) failed(cube string, err error) { r.logf("✗ %s: %v", cube, err) }
func (r *plainReporter) wait()                          {}

// === mpbReporter (terminal interativo: barras ao vivo) ===

type phase int32

const (
	phaseFetching phase = iota // valor zero: estado inicial de todo cubo
	phasePosting
	phaseDone
	phaseError
)

type mpbReporter struct {
	p     *mpb.Progress
	color colorizer
	bars  map[string]*mpb.Bar
	state map[string]*atomic.Int32 // phase por cubo, lido pelo render
}

func newMPBReporter(cubes []string, w io.Writer, color colorizer) *mpbReporter {
	p := mpb.New(mpb.WithOutput(w), mpb.WithWidth(24))
	r := &mpbReporter{
		p:     p,
		color: color,
		bars:  make(map[string]*mpb.Bar, len(cubes)),
		state: make(map[string]*atomic.Int32, len(cubes)),
	}

	style := mpb.BarStyle().Lbound(" ").Filler("█").Tip("█").Padding("░").Rbound(" ")
	if color.enabled {
		style = style.FillerMeta(color.cyan).TipMeta(color.cyan).PaddingMeta(color.dim)
	}

	for _, cube := range cubes {
		r.state[cube] = &atomic.Int32{} // 0 == phaseFetching
		cube := cube
		// total 0 => barra indeterminada: não auto-completa enquanto buscamos.
		bar := p.New(0, style,
			mpb.PrependDecorators(
				decor.Name("  "),
				decor.Name(cube, decor.WCSyncSpaceR),
			),
			mpb.AppendDecorators(
				decor.Any(func(s decor.Statistics) string { return r.status(cube, s) }),
			),
		)
		r.bars[cube] = bar
	}
	return r
}

// status traduz o estado atual do cubo no texto à direita da barra.
func (r *mpbReporter) status(cube string, s decor.Statistics) string {
	switch phase(r.state[cube].Load()) {
	case phaseFetching:
		return r.color.dim("buscando dados…")
	case phaseDone:
		return r.color.green("✓")
	case phaseError:
		return r.color.red("✗ falhou")
	default:
		return fmt.Sprintf("%d/%d lotes", s.Current, s.Total)
	}
}

func (r *mpbReporter) set(cube string, p phase) { r.state[cube].Store(int32(p)) }

func (r *mpbReporter) fetching(cube string) { r.set(cube, phaseFetching) }

func (r *mpbReporter) total(cube string, n int) {
	r.set(cube, phasePosting)
	r.bars[cube].SetTotal(int64(n), false)
}

func (r *mpbReporter) batchDone(cube string) { r.bars[cube].Increment() }

func (r *mpbReporter) succeeded(cube string, _ CubeResult) {
	r.set(cube, phaseDone)
	b := r.bars[cube]
	// Fecha a barra no ponto atual: cobre tanto o caso normal (já cheia) quanto
	// o cubo sem lotes (tudo filtrado), que nunca recebeu um tick.
	b.SetTotal(b.Current(), true)
}

func (r *mpbReporter) failed(cube string, _ error) {
	r.set(cube, phaseError)
	r.bars[cube].Abort(false) // mantém a linha desenhada, mas a dá por encerrada
}

func (r *mpbReporter) wait() { r.p.Wait() }

// === Apresentação (header, passos, resumo) ===

// colorizer aplica ANSI só quando habilitado (terminal). Desabilitado, todos os
// wrappers viram a identidade — nenhum escape vaza para logs não-interativos.
type colorizer struct{ enabled bool }

func (c colorizer) wrap(code, s string) string {
	if !c.enabled {
		return s
	}
	return "\033[" + code + "m" + s + "\033[0m"
}

func (c colorizer) bold(s string) string  { return c.wrap("1", s) }
func (c colorizer) cyan(s string) string  { return c.wrap("36", s) }
func (c colorizer) green(s string) string { return c.wrap("32", s) }
func (c colorizer) red(s string) string   { return c.wrap("31", s) }
func (c colorizer) dim(s string) string   { return c.wrap("2", s) }

// ui cuida da moldura textual em volta das barras: título, passos e resumo.
type ui struct {
	w     io.Writer
	tty   bool
	color colorizer
}

func newUI(f *os.File) *ui {
	tty := isTTY(f)
	return &ui{w: f, tty: tty, color: colorizer{enabled: tty}}
}

func isTTY(f *os.File) bool {
	fi, err := f.Stat()
	return err == nil && fi.Mode()&os.ModeCharDevice != 0
}

func (u *ui) header() {
	fmt.Fprintln(u.w)
	fmt.Fprintln(u.w, "  "+u.color.bold(u.color.cyan("Integração · Score de Uso")))
	fmt.Fprintln(u.w, "  "+u.color.dim("Mitra → Sankhya"))
	fmt.Fprintln(u.w)
}

func (u *ui) step(label string) { fmt.Fprintf(u.w, "  %s… ", label) }
func (u *ui) stepOK()           { fmt.Fprintln(u.w, u.color.green("ok")) }
func (u *ui) stepFail()         { fmt.Fprintln(u.w, u.color.red("falhou")) }

// summary imprime uma linha por cubo e o rodapé com o tempo total, devolvendo o
// erro agregado (se houver) para o Run() propagar.
func (u *ui) summary(cubes []string, results []CubeResult, errs []error, elapsed time.Duration) error {
	width := 0
	for _, c := range cubes {
		if len(c) > width {
			width = len(c)
		}
	}

	fmt.Fprintln(u.w)
	for i, cube := range cubes {
		if errs[i] != nil {
			fmt.Fprintf(u.w, "  %s %-*s %s\n", u.color.red("✗"), width, cube, u.color.dim(errs[i].Error()))
			continue
		}
		r := results[i]
		detail := fmt.Sprintf("%s registros · %d lotes", groupThousands(r.Records), r.Batches)
		if r.Skipped > 0 {
			detail += fmt.Sprintf(" · %d ignorados", r.Skipped)
		}
		fmt.Fprintf(u.w, "  %s %-*s %s\n", u.color.green("✓"), width, cube, u.color.dim(detail))
	}
	fmt.Fprintln(u.w)

	if joined := errors.Join(errs...); joined != nil {
		fmt.Fprintf(u.w, "  %s %s\n", u.color.red("●"), fmt.Sprintf("Concluído com erros em %.2fs", elapsed.Seconds()))
		return fmt.Errorf("erros durante o processamento: %w", joined)
	}
	fmt.Fprintf(u.w, "  %s %s\n", u.color.green("●"), fmt.Sprintf("Tudo concluído em %.2fs", elapsed.Seconds()))
	return nil
}

// groupThousands formata um inteiro com ponto como separador de milhar (pt-BR):
// 1234567 => "1.234.567".
func groupThousands(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	var b strings.Builder
	for i, d := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			b.WriteByte('.')
		}
		b.WriteRune(d)
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}
