package main

import (
	"log"
	"strconv"
)

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
}

func (p ProcessoMitra) ToSankhya() any {
	return ProcessoSankhya{Descricao: p.Descricao}
}

func (s SubprocessoMitra) ToSankhya() any {
	return SubprocessoSankhya{
		Descricao:    s.Descricao,
		LinkPlanilha: s.LinkPlanilha,
	}
}

func (m MapeamentoTelasMitra) ToSankhya() any {
	idProcesso, err := strconv.ParseInt(m.IDProcesso, 10, 16)
	if err != nil {
		log.Fatalln("Erro ao converter o ID Processo em inteiro")
	}

	idSubprocesso, err := strconv.ParseInt(m.IDSubprocesso, 10, 16)
	if err != nil {
		log.Fatalln("Erro ao converter o ID Subprocesso em inteiro")
	}

	return MapeamentoTelasSankhya{
		Descricao:        m.Descricao,
		IDProcesso:       int16(idProcesso),
		IDSubprocesso:    int16(idSubprocesso),
		LinkAjudaSankhya: m.LinkAjudaSankhya,
	}
}

type ProcessoSankhya struct {
	Descricao string `json:"Descrição"`
}

type SubprocessoSankhya struct {
	Descricao    string `json:"Descrição"`
	LinkPlanilha string `json:"Link Planilha"`
}

type MapeamentoTelasSankhya struct {
	Descricao        string `json:"Descrição"`
	IDProcesso       int16  `json:"ID Processo"`
	IDSubprocesso    int16  `json:"ID Subprocesso"`
	LinkAjudaSankhya string `json:"Link Ajuda Sankhya"`
}
