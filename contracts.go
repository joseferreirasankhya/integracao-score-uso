package main

// === Response ===

type Response[T any] struct {
	Content []T
}

// === Mitra ===

type ProcessoMitra struct {
	ID        string `json:"ID"`
	Descricao string `json:"Descrição"`
}

type SubprocessoMitra struct {
	ID           string `json:"ID"`
	Descricao    string `json:"Descrição"`
	LinkPlanilha string `json:"Link Planilha"`
}

type MapeamentoTelasMitra struct {
	ID                        string `json:"ID"`
	Descricao                 string `json:"Descrição"`
	IDSubprocesso             string `json:"ID Subprocesso"`
	DescricaoSubprocesso      string `json:"Descrição Subprocesso"`
	IDProcesso                string `json:"ID Processo"`
	DescricaoProcesso         string `json:"Descrição Processo"`
	IDControleInclusao        string `json:"ID Controle Inclusão"`
	DescricaoControleInclusao string `json:"Descrição Controle Inclusão"`
	LinkAjudaSankhya          string `json:"Link Ajuda Sankhya"`
}

// === Sankhya ===

// SankhyaConvertible is implemented by every Mitra struct that has a Sankhya
// counterpart, instantiating that counterpart for sending.
type SankhyaConvertible interface {
	ToSankhya() any
	GetID() string
}

func (p ProcessoMitra) ToSankhya() any {
	return ProcessoSankhya{
		ID:        p.ID,
		Descricao: p.Descricao,
	}
}

func (s SubprocessoMitra) ToSankhya() any {
	return SubprocessoSankhya{
		ID:           s.ID,
		Descricao:    s.Descricao,
		LinkPlanilha: s.LinkPlanilha,
	}
}

func (m MapeamentoTelasMitra) ToSankhya() any {
	return MapeamentoTelasSankhya{
		ID:               m.ID,
		Descricao:        m.Descricao,
		IDProcesso:       m.IDProcesso,
		IDSubprocesso:    m.IDSubprocesso,
		LinkAjudaSankhya: m.LinkAjudaSankhya,
	}
}

func (p ProcessoMitra) GetID() string {
	return p.ID
}

func (s SubprocessoMitra) GetID() string {
	return s.ID
}

func (m MapeamentoTelasMitra) GetID() string {
	return m.ID
}

type ProcessoSankhya struct {
	ID        string `json:"ID"`
	Descricao string `json:"Descrição"`
}

type SubprocessoSankhya struct {
	ID           string `json:"ID"`
	Descricao    string `json:"Descrição"`
	LinkPlanilha string `json:"Link Planilha"`
}

type MapeamentoTelasSankhya struct {
	ID               string `json:"ID"`
	Descricao        string `json:"Descrição"`
	IDProcesso       string `json:"ID Processo"`
	IDSubprocesso    string `json:"ID Subprocesso"`
	LinkAjudaSankhya string `json:"Link Ajuda Sankhya"`
}
