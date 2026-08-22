package audit

import (
	"path/filepath"
	"testing"
)

func TestLoggerAppendTail(t *testing.T) {
	dir := t.TempDir()
	l, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()

	if err := l.Log("1.2.3.4", "delete", "bafyabc", nil); err != nil {
		t.Fatal(err)
	}
	if err := l.Log("5.6.7.8", "drop_all", "", map[string]string{"keys": "3"}); err != nil {
		t.Fatal(err)
	}
	es, err := l.Tail(10)
	if err != nil {
		t.Fatal(err)
	}
	if len(es) != 2 || es[0].Action != "delete" || es[1].Action != "drop_all" {
		t.Fatalf("Tail = %+v", es)
	}
	if len(es) == 0 && es != nil {
		t.Fatal("unreachable")
	}
}

func TestLoggerNilSafe(t *testing.T) {
	var l *Logger
	if err := l.Log("x", "y", "z", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := l.Tail(5); err != nil {
		t.Fatal(err)
	}
}
