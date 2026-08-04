package commands

import "testing"

func TestRunCatalogFiltersUnknownAndOwnerOnlyMethods(t *testing.T) {
	catalog := Run([]string{"message.history", "message.send_text", "run.cancel", "unknown.method"})
	if len(catalog) != 2 {
		t.Fatalf("Run() = %+v", catalog)
	}
	method, consumed := Resolve([]string{"message", "send_text", "--params-stdin"}, catalog)
	if method != "message.send_text" || consumed != 2 {
		t.Fatalf("Resolve() = %q, %d", method, consumed)
	}
}
