package cmd

import (
	"encoding/json"
	"strings"
	"testing"
)

// A balance has no id, so --quiet is refused rather than silently printing a
// report a script would then try to parse, as on usage.
func TestQuietRefusedOnCreditsAndTheQuotes(t *testing.T) {
	quiet(t)
	for name, args := range map[string][]string{
		"credits":                   {},
		"audiences quote":           {"--file", payloadFile(t, createPayload)},
		"audiences lookalike quote": {"quote", "--file", payloadFile(t, createPayload)},
	} {
		t.Run(name, func(t *testing.T) {
			srv, got := stub(t, creditsEnvelope)
			cmd := creditsCommand()
			switch name {
			case "audiences quote":
				cmd = audiencesQuoteCommand()
			case "audiences lookalike quote":
				cmd = audiencesLookalikeCommand()
			}
			out, _, err := run(t, cmd, srv, args...)
			if err == nil || !strings.Contains(err.Error(), "does not apply") {
				t.Fatalf("err = %v", err)
			}
			if code := exitCode(err, nil, true); code != 2 {
				t.Errorf("exit = %d, want 2", code)
			}
			if out != "" {
				t.Errorf("stdout should stay clean, got %q", out)
			}
			if len(got.paths) != 0 {
				t.Errorf("refusal should cost no round trip, got %v", got.paths)
			}
		})
	}
}

// The balance leads with the period and the figures, and the credits per run
// type follow as their own table.
func TestCreditsShowsTheBalanceAndTheRunTypes(t *testing.T) {
	srv, _ := stub(t, creditsEnvelope)

	out, _, err := run(t, creditsCommand(), srv)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"term 2026-10-01 to 2027-09-30", "4905", "1.9", "2029-11-30", "4905 on 2027-10-01 00:00:00", "audience_build", "Audience Build"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Jane Doe") {
		t.Errorf("the per-user split is for --json, the table leaves it out:\n%s", out)
	}
	if strings.Index(out, "period") > strings.Index(out, "available") {
		t.Errorf("the period leads:\n%s", out)
	}
}

// The statement passes the list flags through and shows the audience by name.
func TestCreditsStatementSearchesAndShowsTheAudienceName(t *testing.T) {
	srv, got := stub(t, statementEnvelope)

	out, errb, err := run(t, creditsCommand(), srv, "statement", "--search", "coffee", "--per-page", "10")
	if err != nil {
		t.Fatal(err)
	}
	if got.paths[0] != "/api/v2/credits/statement" || !strings.Contains(got.queries[0], "search=coffee") || !strings.Contains(got.queries[0], "per_page=10") {
		t.Errorf("path = %s, query = %s", got.paths[0], got.queries[0])
	}
	if !strings.Contains(out, "Coffee Buyers NYC") || !strings.Contains(out, "-95") || !strings.Contains(out, "Plan credits") {
		t.Errorf("output:\n%s", out)
	}
	if !strings.Contains(errb, "2 total") {
		t.Errorf("stderr = %q, want the footer", errb)
	}
}

// A quote from flags posts exactly the body create would send, to the quote
// route, and with no Idempotency-Key: a quote creates nothing.
func TestAudiencesQuotePostsTheCreateBody(t *testing.T) {
	srv, _ := stub(t, providersPOI)
	want := dryRunBody(t, audiencesCreateCommand(), srv, poiFlags...)

	srv2, got := stubSeq(t, reply{body: providersPOI}, reply{body: quoteEnvelope})
	out, _, err := run(t, audiencesQuoteCommand(), srv2, poiFlags...)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.paths) != 2 || got.paths[1] != "/api/v2/analyses/audiences/quote" {
		t.Fatalf("paths = %v", got.paths)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(got.bodies[1]), &sent); err != nil {
		t.Fatal(err)
	}
	wantJSON, _ := json.Marshal(want)
	sentJSON, _ := json.Marshal(sent)
	if string(wantJSON) != string(sentJSON) {
		t.Errorf("quote body = %s\nwant the create body %s", sentJSON, wantJSON)
	}
	if got.keys[1] != "" {
		t.Errorf("Idempotency-Key = %q, want none", got.keys[1])
	}
	for _, want := range []string{"credits", "available_after", "4904", "Apps", "30"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
}

// The lookalike quote takes the create's flags and checks, and posts its body
// to the quote route.
func TestLookalikeQuotePostsTheCreateBody(t *testing.T) {
	srv, _ := stub(t, createdEnvelope)
	want := dryRunBody(t, audiencesLookalikeCommand(), srv, append([]string{"create"}, lookalikeFlags...)...)

	srv2, got := stub(t, lookalikeQuoteEnvelope)
	out, _, err := run(t, audiencesLookalikeCommand(), srv2, append([]string{"quote"}, lookalikeFlags...)...)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.paths) != 1 || got.paths[0] != "/api/v2/analyses/audiences/quote-lookalike" {
		t.Fatalf("paths = %v", got.paths)
	}
	var sent map[string]any
	if err := json.Unmarshal([]byte(got.bodies[0]), &sent); err != nil {
		t.Fatal(err)
	}
	wantJSON, _ := json.Marshal(want)
	sentJSON, _ := json.Marshal(sent)
	if string(wantJSON) != string(sentJSON) {
		t.Errorf("quote body = %s\nwant the create body %s", sentJSON, wantJSON)
	}
	if got.keys[0] != "" {
		t.Errorf("Idempotency-Key = %q, want none", got.keys[0])
	}
	if !strings.Contains(out, "120") || !strings.Contains(out, "880") {
		t.Errorf("output:\n%s", out)
	}

	// The create's checks run on the quote too, before any round trip.
	srv3, got3 := stub(t, lookalikeQuoteEnvelope)
	_, _, err = run(t, audiencesLookalikeCommand(), srv3, append([]string{"quote"}, swap(lookalikeFlags, "--target-size", "0")...)...)
	if code := exitCode(err, nil, true); code != 2 || len(got3.paths) != 0 {
		t.Errorf("exit = %d, paths = %v; want a usage error with no round trip", code, got3.paths)
	}
}
