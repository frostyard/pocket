package pocket

import (
	"encoding/json"
	"testing"
)

func update(t *testing.T, s string) *ViewUpdate {
	t.Helper()
	var u ViewUpdate
	if err := json.Unmarshal([]byte(s), &u); err != nil {
		t.Fatal(err)
	}
	return &u
}

func TestViewAppliesFullThenUpdates(t *testing.T) {
	v := NewView()
	err := v.Apply(update(t, `{"full":true,"conversation":{"id":3,"title":"T"},
		"entries":[{"id":13,"kind":"user","text":"hi"},{"id":18,"kind":"assistant","blocks":[{"type":"text","text":"echo"}]}],
		"order":[13,18],"live":{"busy":false},"approvals":[],"branch":{"branch":"main"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Rows()) != 2 || v.Branch == nil || v.Branch.Branch != "main" {
		t.Fatalf("rows %d branch %+v", len(v.Rows()), v.Branch)
	}
	// An update with new entries and the order's tail.
	err = v.Apply(update(t, `{"full":false,"conversation":{"id":3,"title":"T"},
		"entries":[{"id":19,"kind":"user","text":"more"},{"id":20,"kind":"toolResult","callId":"c1","text":"ok"}],
		"order":[19,20],"orderFrom":2,"live":{"busy":true},"approvals":[],"branch":null}`))
	if err != nil {
		t.Fatal(err)
	}
	if got := len(v.Order); got != 4 {
		t.Fatalf("order = %v", v.Order)
	}
	if len(v.Rows()) != 3 || v.Results()["c1"] == nil {
		t.Fatalf("rows %d results %v", len(v.Rows()), v.Results())
	}
	if v.Branch != nil {
		t.Fatalf("branch null should clear it, got %+v", v.Branch)
	}
	// A compaction drops entries out of the order.
	err = v.Apply(update(t, `{"full":false,"conversation":{"id":3},"entries":[],"order":[19,20],"live":{"busy":false},"approvals":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Entries) != 2 || v.Entries[13] != nil {
		t.Fatalf("entries after reorder: %v", v.Entries)
	}
}

func TestLiveDeltasGrowTheMessage(t *testing.T) {
	v := NewView()
	base := `{"full":true,"conversation":{"id":1},"entries":[],"order":[],"approvals":[],"live":{"busy":true,"generation":{"attempt":1,"message":{"blocks":[{"type":"thinking","text":"hmm"},{"type":"text","text":"Hel"}]}}}}`
	if err := v.Apply(update(t, base)); err != nil {
		t.Fatal(err)
	}
	if err := v.Apply(update(t, `{"conversation":{"id":1},"entries":[],"approvals":[],"live":{"busy":true,"generation":{"attempt":1,"message":{"keep":1,"blocks":[{"type":"text","from":3,"text":"lo"},{"type":"toolCall","id":"c","name":"bash","args":{"command":"ls"}}]}}}}`)); err != nil {
		t.Fatal(err)
	}
	b := v.Live.Generation.Message.Blocks
	if len(b) != 3 || b[1].Text != "Hello" || b[2].Name != "bash" || b[0].Text != "hmm" {
		t.Fatalf("blocks = %+v", b)
	}
	// A delta that does not fit asks for a resync and keeps the message.
	err := v.Apply(update(t, `{"conversation":{"id":1},"entries":[],"approvals":[],"live":{"busy":true,"generation":{"attempt":1,"message":{"keep":1,"blocks":[{"type":"text","from":99,"text":"x"}]}}}}`))
	if err != ErrResync {
		t.Fatalf("err = %v", err)
	}
	if v.Live.Generation.Message.Blocks[1].Text != "Hello" {
		t.Fatalf("kept = %+v", v.Live.Generation.Message.Blocks)
	}
}

func TestPartialEntryDoesNotReplaceWhole(t *testing.T) {
	v := NewView()
	_ = v.Apply(update(t, `{"full":true,"conversation":{"id":1},"entries":[{"id":5,"kind":"toolResult","callId":"a","text":"whole"}],"order":[5],"live":{"busy":false},"approvals":[]}`))
	_ = v.Apply(update(t, `{"conversation":{"id":1},"entries":[{"id":5,"kind":"toolResult","callId":"a","text":"","partial":true}],"live":{"busy":false},"approvals":[]}`))
	if v.Entries[5].Text != "whole" {
		t.Fatalf("entry = %+v", v.Entries[5])
	}
}

func TestDeskDecodesWatchResultsAndSurvivesDrift(t *testing.T) {
	var v DeskView
	// A watch that passed, as desk.ts sends it, and a field in a shape
	// this client does not expect (closed.at as a string).
	err := Decode([]byte(`{"threads":[
		{"id":"T1","title":"a","watch":{"checkOn":"2026-11-14","check":"c","setBy":"brian","due":true,
			"result":{"pass":true,"text":"ok","by":"brian","at":1}}},
		{"id":"T2","title":"b","closed":{"at":"yesterday","why":"done","by":"sweep"}}],
		"counts":{"open":2}}`), &v)
	if err == nil {
		t.Fatal("want the drift reported")
	}
	if len(v.Threads) != 2 || v.Threads[0].Watch.Result == nil || !v.Threads[0].Watch.Result.Pass || v.Counts.Open != 2 {
		t.Fatalf("the rest was not decoded: %+v", v)
	}
}
