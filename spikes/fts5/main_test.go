package main

import (
	"os"
	"testing"
)

func TestSpike(t *testing.T) {
	if err := run(os.Stdout); err != nil {
		t.Fatal(err)
	}
}
