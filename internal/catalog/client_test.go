package catalog

import "testing"

func TestWritesDeleted(t *testing.T) {
	var c Client
	if _, err := c.CreateEntity(nil, nil, "", nil); err != ErrWritesDeleted {
		t.Fatalf("got %v want %v", err, ErrWritesDeleted)
	}
}
