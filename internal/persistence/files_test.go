package persistence

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestAtomicJSONAndJournal(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "checkpoint.json")
	want := map[string]int{"completed": 3}
	if err := WriteJSONAtomic(path, want); err != nil {
		t.Fatal(err)
	}
	var got map[string]int
	if err := ReadJSON(path, &got); err != nil || got["completed"] != 3 {
		t.Fatalf("read = %v/%v", got, err)
	}
	journal, err := OpenJournal(filepath.Join(directory, "progress.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(map[string]int{"sequence": 1}); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(filepath.Join(directory, "progress.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	if !scanner.Scan() {
		t.Fatal("journal is empty")
	}
	var event map[string]int
	if err := json.Unmarshal(scanner.Bytes(), &event); err != nil || event["sequence"] != 1 {
		t.Fatalf("event = %v/%v", event, err)
	}
}

func TestOpenJournalRepairsPartialLastRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "progress.jsonl")
	if err := os.WriteFile(path, []byte("{\"sequence\":1}\n{\"sequence\":"), 0o644); err != nil {
		t.Fatal(err)
	}
	journal, err := OpenJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(map[string]int{"sequence": 2}); err != nil {
		t.Fatal(err)
	}
	if err := journal.Close(); err != nil {
		t.Fatal(err)
	}
	var last map[string]int
	if err := ReadLastJSONLine(path, &last); err != nil || last["sequence"] != 2 {
		t.Fatalf("last = %v/%v", last, err)
	}
}
