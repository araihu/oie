package sqlite

import (
	"strings"
	"testing"
)

func TestSharedMetadataSearchPolicy(t *testing.T) {
	for _, tc := range []struct {
		text, query string
		want        bool
	}{
		{"anything", "", true},
		{"Relatório de ORÇAMENTO", "relatorio orcamento", true},
		{"A cafe\u0301 receipt Straße", "CAFÉ STRASSE", true},
		{"Relatório de orçamento", "orcamento relatorio", true},
		{"A video recording", "viedo", true},
		{"A document attached", "docun", true},
		{"faturamento", "faturamneto", true},
		{"faturamento", "fatxramenxo", true},
		{"faturamento", "fatxramynxo", false},
		{"A video recording", "vxdxo", false},
		{"A cat", "cta", false},
		{"invoice 2026", "2025", false},
		{"invoice 12345678", "12345679", false},
		{"A café 😀 receipt", "cafe 😀", true},
		{"A café receipt", "cafe 😀", false},
		{"A café 😀 receipt", "😢", false},
		{"A report.pdf", "pdf", true},
		{"abx", "abcd", false},
		{"anything", "...", false},
		{"anything", "\u0301", false},
		{"anything", strings.Repeat("a", 201), false},
	} {
		t.Run(tc.query+"/"+tc.text, func(t *testing.T) {
			if got := matchSharedMetadata(tc.text, tc.query); got != tc.want {
				t.Errorf("matchSharedMetadata(%q,%q)=%v, want %v", tc.text, tc.query, got, tc.want)
			}
		})
	}
}

func BenchmarkSharedMetadataSearch(b *testing.B) {
	text := "https://example.org/media/arquivo-2026.pdf Relatório de orçamento e faturamento, enviado por Ana na conversa Família, conta Pessoal"
	for b.Loop() {
		if !matchSharedMetadata(text, "orcamento faturamneto") {
			b.Fatal("expected fuzzy match")
		}
	}
}
