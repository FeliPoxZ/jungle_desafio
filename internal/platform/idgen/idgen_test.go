package idgen

import (
	"testing"

	"github.com/google/uuid"
)

func TestNewIDGeraUUIDValidoEUnico(t *testing.T) {
	g := New()

	vistos := make(map[string]bool, 1000)
	for i := 0; i < 1000; i++ {
		id := g.NewID()

		if _, err := uuid.Parse(id); err != nil {
			t.Fatalf("id %q não é UUID válido: %v", id, err)
		}
		if vistos[id] {
			t.Fatalf("id repetido: %q", id)
		}
		vistos[id] = true
	}
}

func TestNewIDEhVersao7(t *testing.T) {
	// v7 é o que dá a ordenação temporal. Se alguém trocar por v4 sem perceber,
	// o índice primário perde a localidade de inserção.
	id, err := uuid.Parse(New().NewID())
	if err != nil {
		t.Fatal(err)
	}
	if v := id.Version(); v != 7 {
		t.Errorf("versão do UUID = %d, esperado 7", v)
	}
}

func TestNewIDCresceNoTempo(t *testing.T) {
	// Propriedade que justifica a escolha: IDs gerados em sequência ordenam
	// lexicalmente na mesma ordem em que foram criados.
	g := New()

	anterior := g.NewID()
	for i := 0; i < 100; i++ {
		atual := g.NewID()
		if atual <= anterior {
			t.Fatalf("id fora de ordem: %q veio depois de %q", atual, anterior)
		}
		anterior = atual
	}
}
