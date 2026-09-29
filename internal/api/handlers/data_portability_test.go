package handlers

import "testing"

func TestRequestedExportEntitiesIncludesCompetitionResults(t *testing.T) {
	defaults, err := requestedExportEntities("")
	if err != nil {
		t.Fatalf("requestedExportEntities(default): %v", err)
	}
	for _, excluded := range []string{"submissions", "ledger_history"} {
		for _, entity := range defaults {
			if entity == excluded {
				t.Fatalf("large or sensitive entity %q was included by default", excluded)
			}
		}
	}
	entities, err := requestedExportEntities("scoreboard,solves,submissions,ledger_balances,ledger_history")
	if err != nil {
		t.Fatalf("requestedExportEntities(): %v", err)
	}
	want := []string{"scoreboard", "solves", "submissions", "ledger_balances", "ledger_history"}
	if len(entities) != len(want) {
		t.Fatalf("entities=%v want=%v", entities, want)
	}
	for index := range want {
		if entities[index] != want[index] {
			t.Fatalf("entities=%v want=%v", entities, want)
		}
	}
	if _, err := requestedExportEntities("scoreboard,secrets"); err == nil {
		t.Fatal("unsupported export entity was accepted")
	}
}

func TestParseImportJSONAcceptsPortableShapes(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "array", content: `[{"slug":"crypto","name":"Crypto"}]`},
		{name: "rows", content: `{"rows":[{"slug":"crypto","name":"Crypto"}]}`},
		{name: "entity", content: `{"categories":[{"slug":"crypto","name":"Crypto"}]}`},
		{name: "anvil export", content: `{"format":"anvil-data","data":{"categories":{"headers":["slug","name"],"rows":[{"slug":"crypto","name":"Crypto"}]}}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			rows, issues := parseImportJSON("categories", []byte(test.content))
			if len(issues) != 0 || len(rows) != 1 || rows[0]["slug"] != "crypto" || rows[0]["name"] != "Crypto" {
				t.Fatalf("rows=%v issues=%v", rows, issues)
			}
		})
	}
}

func TestParseImportJSONReturnsActionableShapeErrors(t *testing.T) {
	_, issues := parseImportJSON("challenges", []byte(`{"data":{"categories":{"rows":[]}}}`))
	if len(issues) != 1 || issues[0].Message == "" {
		t.Fatalf("issues=%v", issues)
	}
	_, issues = parseImportJSON("challenges", []byte(`["not-an-object"]`))
	if len(issues) != 1 || issues[0].Message != "JSON row 1 must be an object" {
		t.Fatalf("issues=%v", issues)
	}
}

func TestAnonymizeCompetitionExports(t *testing.T) {
	collection := exportCollection{Rows: []map[string]any{{
		"username": "alice", "team_name": "red", "ip_address": "192.0.2.4", "user_agent": "browser",
	}}}
	anonymizeCollection("submissions", &collection)
	row := collection.Rows[0]
	if row["username"] == "alice" || row["team_name"] == "red" || row["ip_address"] != "" || row["user_agent"] != "" {
		t.Fatalf("submission export was not anonymized: %v", row)
	}
}
