package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestSingleFileCLI(t *testing.T) {
	cache, err := os.UserCacheDir()
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		args     []string
		want     string
		exitCode int
	}{
		{[]string{"-h"}, "-template-file <string>", 0},
		{nil, "source text", 1},
		{[]string{"-threads", "1"}, "source text", 1},
		{[]string{"-file"}, "flag needs an argument", 1},
	} {
		cmd := exec.Command("go", append([]string{"run", "main.go"}, tc.args...)...)
		var out, diagnostics bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &diagnostics
		if err := cmd.Run(); err != nil && tc.exitCode == 0 {
			t.Fatalf("%v: %v\n%s", tc.args, err, &diagnostics)
		}
		if cmd.ProcessState.ExitCode() != tc.exitCode {
			t.Fatalf("%v: exit=%d, want=%d\n%s", tc.args, cmd.ProcessState.ExitCode(), tc.exitCode, &diagnostics)
		}
		if out.Len() != 0 {
			t.Fatalf("help polluted translation output: %q", out.String())
		}
		help := strings.Join(strings.Fields(diagnostics.String()), " ")
		for _, want := range []string{
			"Usage: local-translate", "Description:", "Default model", "Options:", "Examples:",
			"Model storage: " + filepath.Join(cache, "local-translate"),
			"4B parameters", "Q4_K_M (4-bit)", "Show help", "GGUF size",
			"Qwen3-4B", "~2.50 GB", defaultModelURL, "-model-url <string>", "-debug", tc.want,
		} {
			if !strings.Contains(help, want) {
				t.Errorf("%v: help missing %q:\n%s", tc.args, want, &diagnostics)
			}
		}
		for _, removed := range []string{"-mode <", "QWEN_MODEL", "Gemma", "preset"} {
			if strings.Contains(diagnostics.String(), removed) {
				t.Errorf("%v: help still contains %q", tc.args, removed)
			}
		}
	}
}

func TestCLIEnvironment(t *testing.T) {
	for _, tc := range []struct{ name, option, valid string }{
		{"LOCAL_TRANSLATE_CTX", "ctx", "8192"},
		{"LOCAL_TRANSLATE_MAX_TOKENS", "max-tokens", "256"},
		{"LOCAL_TRANSLATE_THREADS", "threads", "1"},
		{"LOCAL_TRANSLATE_DEBUG", "debug", "false"},
		{"LOCAL_TRANSLATE_HELP", "help", "false"},
		{"LOCAL_TRANSLATE_HELP", "h", "false"},
		{"LOCAL_TRANSLATE_GLOSSARY", "glossary", "source:target"},
	} {
		t.Run(tc.option, func(t *testing.T) {
			t.Setenv(tc.name, "invalid")
			err := run(nil, strings.NewReader("Hello"), io.Discard, io.Discard, nil)
			if err == nil || !strings.Contains(err.Error(), tc.name) {
				t.Fatalf("environment value was ignored: %v", err)
			}
			err = run([]string{"-" + tc.option + "=" + tc.valid, "-model-url", "invalid"}, strings.NewReader("Hello"), io.Discard, io.Discard, nil)
			if err == nil || !strings.Contains(err.Error(), "-model-url") || strings.Contains(err.Error(), tc.name) {
				t.Fatalf("CLI did not override invalid environment value: %v", err)
			}
		})
	}
	t.Run("help", func(t *testing.T) {
		t.Setenv("LOCAL_TRANSLATE_HELP", "true")
		var out, diagnostics bytes.Buffer
		if err := run(nil, strings.NewReader(""), &out, &diagnostics, nil); err != nil || out.Len() != 0 {
			t.Fatalf("environment help: %v, stdout=%q", err, &out)
		}
		for _, want := range []string{"Environment:", "replace '-' with '_'", "-context -> LOCAL_TRANSLATE_CONTEXT"} {
			if !strings.Contains(diagnostics.String(), want) {
				t.Errorf("help missing environment guidance %q", want)
			}
		}
		if strings.Contains(diagnostics.String(), "[env:") {
			t.Fatal("help repeats environment annotations on individual options")
		}
	})
	t.Run("empty", func(t *testing.T) {
		t.Setenv("LOCAL_TRANSLATE_THREADS", "")
		err := run([]string{"-model-url", "invalid"}, strings.NewReader("Hello"), io.Discard, io.Discard, nil)
		if err == nil || !strings.Contains(err.Error(), "-model-url") {
			t.Fatalf("empty environment value was not ignored: %v", err)
		}
	})
}

func TestCLIEnvTemplate(t *testing.T) {
	spec, err := getModel(defaultModelURL, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(defaultModel(spec)); os.IsNotExist(err) {
		t.Skip("model not downloaded")
	}
	dir := t.TempDir()
	file, template := filepath.Join(dir, "input.txt"), filepath.Join(dir, "template.jinja")
	if err := os.WriteFile(file, []byte("Open the dashboard."), 0o600); err != nil {
		t.Fatal(err)
	}
	source := `{% if target_lang != 'ja' or source_lang != 'en' or form != 'polite' or context != 'Help page.\nReference context.' or messages[1]['content'] != 'Open the dashboard.' %}{{ raise_exception('incorrect scalar options') }}{% endif %}{% if glossary|length != 2 or glossary[0]['source'] != 'a:a' or glossary[0]['target'] != 'b:b' or glossary[1]['source'] != 'owner' or glossary[1]['target'] != '担当者' %}{{ raise_exception('incorrect glossary') }}{% endif %}{{ raise_exception('environment template selected') }}`
	if err := os.WriteFile(template, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{
		"MODEL_URL": defaultModelURL, "SHA256": defaultSHA256, "FILE": file, "TEMPLATE_FILE": template,
		"FROM": "en", "TO": "ja", "FORM": "polite", "CONTEXT": "Help page.\nReference context.",
		"GLOSSARY": "a\\:a:b\\:b\n\nowner:担当者\n", "CTX": "512", "MAX_TOKENS": "128", "THREADS": "8", "DEBUG": "true", "HELP": "false",
	} {
		t.Setenv("LOCAL_TRANSLATE_"+name, value)
	}
	var out, diagnostics bytes.Buffer
	err = run(nil, strings.NewReader("Wrong source."), &out, &diagnostics, nil)
	if err == nil || !strings.Contains(err.Error(), "environment template selected") || out.Len() != 0 || !strings.Contains(diagnostics.String(), "Loading model:") {
		t.Fatalf("environment options: %v, stdout=%q, stderr=%q", err, &out, &diagnostics)
	}
	// Explicit CLI values replace (rather than merge with) environment configuration.
	source = `{% if context != 'CLI context.' or glossary|length != 1 or glossary[0]['source'] != 'cli' or glossary[0]['target'] != 'value' %}{{ raise_exception('incorrect CLI override') }}{% endif %}{{ raise_exception('CLI options selected') }}`
	if err := os.WriteFile(template, []byte(source), 0o600); err != nil {
		t.Fatal(err)
	}
	diagnostics.Reset()
	err = run([]string{"-debug=false", "-context", "CLI context.", "-glossary", "cli:value"}, strings.NewReader("Wrong source."), &out, &diagnostics, nil)
	if err == nil || !strings.Contains(err.Error(), "CLI options selected") || diagnostics.Len() != 0 {
		t.Fatalf("CLI override: %v, stderr=%q", err, &diagnostics)
	}
}

func TestCLIModels(t *testing.T) {
	for _, args := range [][]string{nil, {"-model-url", defaultModelURL}, {"-debug"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			spec, err := getModel(defaultModelURL, "")
			if err != nil {
				t.Fatal(err)
			}
			path := defaultModel(spec)
			if _, err := os.Stat(path); os.IsNotExist(err) {
				t.Skip("model not downloaded")
			} else if err != nil {
				t.Fatal(err)
			}

			var out, diagnostics bytes.Buffer
			err = run(append(args, "-threads", "8", "-max-tokens", "128", "This system is convenient."), strings.NewReader(""), &out, &diagnostics, nil)
			if err != nil {
				t.Fatalf("%v\n%s", err, &diagnostics)
			}
			if strings.Contains(diagnostics.String(), "Warning: GGUF has no chat template") {
				t.Fatal("default model unexpectedly used the plain-text fallback")
			}
			if !slices.Contains(args, "-debug") && diagnostics.Len() != 0 {
				t.Fatalf("normal translation wrote diagnostics: %q", &diagnostics)
			}
			if slices.Contains(args, "-debug") {
				for _, want := range []string{"Loading model: Qwen3-4B-Q4_K_M.gguf (CPU inference)...", "Translating chunk 1/1..."} {
					if !strings.Contains(diagnostics.String(), want) {
						t.Errorf("debug output missing %q: %s", want, &diagnostics)
					}
				}
				for _, prefix := range []string{"Model loaded in ", "Chunk 1/1 translated in ", "Total time: "} {
					_, rest, found := strings.Cut(diagnostics.String(), prefix)
					elapsed, _, _ := strings.Cut(rest, "\n")
					duration, err := time.ParseDuration(elapsed)
					if !found || err != nil || duration <= 0 {
						t.Errorf("missing or invalid elapsed time for %q: %s", prefix, &diagnostics)
					}
				}
			}
			translation := strings.TrimSpace(out.String())
			if !strings.ContainsFunc(translation, func(r rune) bool {
				return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana)
			}) {
				t.Fatalf("Japanese translation missing: %q", translation)
			}
			for _, control := range []string{"<think>", "</think>", "<|", "<turn|>"} {
				if strings.Contains(translation, control) {
					t.Fatalf("template control leaked into translation: %q", translation)
				}
			}
		})
	}
}

func TestCLILongInput(t *testing.T) {
	spec, err := getModel(defaultModelURL, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(defaultModel(spec)); os.IsNotExist(err) {
		t.Skip("model not downloaded")
	} else if err != nil {
		t.Fatal(err)
	}
	// Japanese in the last paragraph must select English for every chunk.
	text := "The invoice number is 73142. The payment is due on Friday.\n\n" +
		"The invoice number is 28659. The payment is due on Monday.\n\n" +
		"The invoice number is 94017. The payment is due on Tuesday.\n\n" +
		"請求書番号は50368です。支払期限は木曜日です。"
	var out bytes.Buffer
	// An omitted output budget must follow the explicitly selected context size.
	err = run([]string{"-threads", "8", "-ctx", "256"}, strings.NewReader(text), &out, io.Discard, nil)
	if err != nil {
		t.Fatal(err)
	}
	got := out.String()
	previous := -1
	for _, id := range []string{"73142", "28659", "94017", "50368"} {
		position := strings.Index(got, id)
		if strings.Count(got, id) != 1 || position <= previous {
			t.Fatalf("missing, repeated, or reordered invoice %s: %q", id, got)
		}
		previous = position
	}
	if strings.ContainsFunc(got, func(r rune) bool {
		return unicode.In(r, unicode.Han, unicode.Hiragana, unicode.Katakana)
	}) {
		t.Fatalf("translation direction changed between chunks: %q", got)
	}
}

func TestAutoDirection(t *testing.T) {
	for _, tc := range []struct {
		text, source, target, want string
	}{
		{"こんにちは。", "", "auto", "en"},
		{"承認済", "", "auto", "en"},
		{"ﾃｽﾄ", "", "auto", "en"},
		{"Hello, world.", "", "auto", "ja"},
		{"Hello", "ja", "auto", "en"},
		{"Hello", "JA", "auto", "en"},
		{"日本語", "en", "auto", "ja"},
		{"こんにちは。", "", "ja", "ja"},
		{"Hello", "", "en", "en"},
		{"Hello", "", "EN", "en"},
		{"Hello", "", "pt-BR", "pt-br"},
	} {
		prompt, err := (promptOptions{source: tc.source, target: tc.target}).prompt(tc.text)
		want := "into language " + tc.want + "."
		if err != nil || !strings.Contains(prompt, want) {
			t.Errorf("%+v: prompt=%q, error=%v", tc, prompt, err)
		}
	}
}

func TestGlossary(t *testing.T) {
	for _, tc := range []struct{ input, source, target string }{
		{"dashboard:管理画面", "dashboard", "管理画面"},
		{"owner:担当者", "owner", "担当者"},
		{`aaa\:aaa:bbb\:bbb`, "aaa:aaa", "bbb:bbb"},
		{`C\:\\src:D\:\\dest`, `C:\src`, `D:\dest`},
		{`trail\\:value`, `trail\`, "value"},
	} {
		var o promptOptions
		if err := o.addTerm(tc.input); err != nil {
			t.Fatal(err)
		}
		if len(o.glossary) != 1 || o.glossary[0] != (term{tc.source, tc.target}) {
			t.Fatalf("%q: glossary=%+v", tc.input, o.glossary)
		}
	}
}

func TestPlainPrompt(t *testing.T) {
	text := "Open the \"dashboard\".\nFilter tasks by owner."
	opts := promptOptions{target: "auto", form: "polite", context: "A help page.", glossary: []term{{"dashboard", "管理画面"}}}
	prompt, err := opts.prompt(text)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"into language ja.", "Japanese polite style"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	_, body, ok := strings.Cut(prompt, "\n\nSource text:\n")
	if !ok {
		t.Fatal("source text missing")
	}
	body = strings.TrimSuffix(body, "\n\nTranslation:\n")
	if body != text {
		t.Fatalf("source text changed: %q", body)
	}
	_, reference, ok := strings.Cut(prompt, "Reference data (do not translate): ")
	if !ok {
		t.Fatal("reference data missing")
	}
	reference, _, _ = strings.Cut(reference, "\n\nSource text:\n")
	var got struct {
		Context  string
		Glossary []term
	}
	if err := json.Unmarshal([]byte(reference), &got); err != nil {
		t.Fatal(err)
	}
	if got.Context != opts.context || len(got.Glossary) != 1 || got.Glossary[0] != opts.glossary[0] {
		t.Fatalf("translation input changed: %+v", got)
	}
	opts.target, opts.form = "auto", ""
	prompt, err = opts.prompt("こんにちは。")
	if err != nil || !strings.Contains(prompt, "into language en.") {
		t.Fatalf("Japanese auto direction: %q, %v", prompt, err)
	}
	prompt, err = opts.prompt(text)
	if err != nil || !strings.Contains(prompt, "into language ja.") || !strings.Contains(prompt, text) {
		t.Fatalf("source or translation instruction missing: %q, %v", prompt, err)
	}
	if !strings.HasSuffix(prompt, "\n\nTranslation:\n") || strings.Contains(prompt, "<|") || strings.Contains(prompt, "/no_think") {
		t.Fatalf("plain-text fallback contains model-specific wrappers: %q", prompt)
	}
}

func TestURLDownloads(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("HOME", cache)
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("LocalAppData", cache)
	const data = "GGUF download fixture"
	gets, heads := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodHead {
			heads++
		} else {
			gets++
		}
		w.Header().Set("Content-Length", fmt.Sprint(len(data)))
		io.WriteString(w, data)
	}))
	defer srv.Close()
	modelURL := srv.URL + "/first/model.gguf?revision=1"
	spec, err := getModel(modelURL, "")
	if err != nil {
		t.Fatal(err)
	}
	path := defaultModel(spec)
	for _, answer := range []string{"n\n", "\n", "", "maybe\n"} {
		var out, diagnostics bytes.Buffer
		err := run([]string{"-model-url", modelURL}, strings.NewReader("Hello"), &out, &diagnostics, strings.NewReader(answer))
		if err == nil || !strings.Contains(err.Error(), "canceled") || gets != 0 || out.Len() != 0 {
			t.Fatalf("downloaded without approval %q: %v, GETs=%d, stdout=%q", answer, err, gets, &out)
		}
		for _, want := range []string{modelURL, fmt.Sprintf("(%d bytes)", len(data)), "Destination: " + path, "Download this model? [y/N]"} {
			if !strings.Contains(diagnostics.String(), want) {
				t.Errorf("confirmation missing %q: %s", want, &diagnostics)
			}
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("unapproved model was cached")
		}
	}
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(data)))
	err = run([]string{"-model-url", modelURL, "-sha256", checksum}, strings.NewReader("Hello"), io.Discard, io.Discard, strings.NewReader("YES\n"))
	if err == nil || !strings.Contains(err.Error(), "loading model") || gets != 1 {
		t.Fatalf("approved fixture did not reach model loader: %v, GETs=%d", err, gets)
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != data {
		t.Fatalf("cache: %q, %v", got, err)
	}
	before := heads
	err = run([]string{"-model-url", modelURL}, strings.NewReader("Hello"), io.Discard, io.Discard, strings.NewReader(""))
	if err == nil || !strings.Contains(err.Error(), "loading model") || heads != before || gets != 1 {
		t.Fatalf("cache reuse accessed network: %v, HEADs=%d, GETs=%d", err, heads, gets)
	}
	err = run([]string{"-model-url", modelURL, "-sha256", strings.Repeat("0", 64)}, strings.NewReader("Hello"), io.Discard, io.Discard, nil)
	if err == nil || !strings.Contains(err.Error(), "cached model SHA-256 mismatch") {
		t.Fatalf("cached digest ignored: %v", err)
	}
	for _, otherURL := range []string{srv.URL + "/second/model.gguf?revision=1", srv.URL + "/first/model.gguf?revision=2"} {
		other, err := getModel(otherURL, "")
		if err != nil {
			t.Fatal(err)
		}
		if defaultModel(other) == path {
			t.Fatal("different URLs share a cache entry")
		}
		if err := run([]string{"-model-url", otherURL}, strings.NewReader("Hello"), io.Discard, io.Discard, strings.NewReader("n\n")); err == nil || !strings.Contains(err.Error(), "canceled") {
			t.Fatalf("different URL reused existing model: %v", err)
		}
	}
}

func TestDefaultDownload(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("HOME", cache)
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("LocalAppData", cache)
	transport := http.DefaultTransport
	t.Cleanup(func() { http.DefaultTransport = transport })
	http.DefaultTransport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
		t.Errorf("default confirmation unexpectedly accessed network: %s", r.URL)
		return nil, fmt.Errorf("unexpected network access")
	})
	var out, diagnostics bytes.Buffer
	err := run(nil, strings.NewReader("Hello"), &out, &diagnostics, strings.NewReader("n\n"))
	if err == nil || !strings.Contains(err.Error(), "canceled") || out.Len() != 0 {
		t.Fatalf("%v, stdout=%q", err, &out)
	}
	for _, want := range []string{defaultModelURL, "2.50 GB (2497280256 bytes)", "Qwen3-4B-Q4_K_M.gguf", "Destination:"} {
		if !strings.Contains(diagnostics.String(), want) {
			t.Errorf("default confirmation missing %q: %s", want, &diagnostics)
		}
	}
}

func TestDownloadMetadata(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("HOME", cache)
	t.Setenv("XDG_CACHE_HOME", cache)
	t.Setenv("LocalAppData", cache)
	const data = "GGUF download fixture"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing.gguf" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.Method == http.MethodHead {
			if r.URL.Path == "/signed.gguf" {
				w.WriteHeader(http.StatusForbidden)
				return
			}
			if r.URL.Path == "/changed.gguf" {
				w.Header().Set("Content-Length", "12")
				return
			}
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		w.(http.Flusher).Flush()
		if r.URL.Path == "/html.gguf" {
			io.WriteString(w, "<html>Not a model</html>")
			return
		}
		io.WriteString(w, data)
	}))
	defer srv.Close()
	for _, tc := range []struct{ file, want string }{
		{"unknown.gguf", ""},
		{"signed.gguf", ""},
		{"html.gguf", "not a GGUF"},
		{"changed.gguf", "size mismatch"},
		{"missing.gguf", "HTTP 404"},
	} {
		spec, err := getModel(srv.URL+"/"+tc.file, "")
		if err != nil {
			t.Fatal(err)
		}
		var diagnostics bytes.Buffer
		err = downloadModel(spec, &diagnostics, strings.NewReader("y\n"))
		if tc.want != "" {
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("%s: %v", tc.file, err)
			}
			if _, err := os.Stat(defaultModel(spec)); !os.IsNotExist(err) {
				t.Fatalf("invalid download was cached: %s", tc.file)
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(defaultModel(spec))
		if err != nil || string(got) != data {
			t.Fatalf("unknown-size download: %q, %v", got, err)
		}
		for _, want := range []string{"Size: unknown", "total unknown", "not publisher-verified"} {
			if !strings.Contains(diagnostics.String(), want) {
				t.Errorf("diagnostics missing %q: %s", want, &diagnostics)
			}
		}
	}
}

func TestCLIRejectsInvalidInput(t *testing.T) {
	for _, tc := range []struct {
		args []string
		text string
		want string
	}{
		{nil, " \n", "source text is empty"},
		{[]string{"-mode", "unknown"}, "Hello", "-mode"},
		{[]string{"-model-url", "file:///tmp/model.gguf"}, "Hello", "-model-url"},
		{[]string{"-model-url", "https://example.org/card"}, "Hello", "-model-url"},
		{[]string{"-model-url", "https://example.org/model.gguf#fragment"}, "Hello", "-model-url"},
		{[]string{"-model-url", "https://user:password@example.org/model.gguf"}, "Hello", "-model-url"},
		{[]string{"-sha256", "abcd"}, "Hello", "-sha256"},
		{[]string{"-model", "unused.gguf"}, "Hello", "flag provided but not defined"},
		{[]string{"-glossary", "aaa"}, "Hello", "source:target"},
		{[]string{"-glossary", "aaa:bbb:ccc"}, "Hello", "source:target"},
		{[]string{"-glossary", `aaa\:bbb`}, "Hello", "source:target"},
		{[]string{"-glossary", " :bbb"}, "Hello", "source:target"},
		{[]string{"-glossary", "aaa:"}, "Hello", "source:target"},
		{[]string{"-glossary", `aaa:bbb\`}, "Hello", "source:target"},
		{[]string{"-glossary", `aaa\q:bbb`}, "Hello", "source:target"},
		{[]string{"-to", "ja_XX"}, "Hello", "language code"},
		{[]string{"-to", "ja en"}, "Hello", "language code"},
		{[]string{"-to", "en", "-form", "polite"}, "Hello", "-form"},
		{[]string{"-ctx", "64", "-max-tokens", "64"}, "Hello", "max-tokens"},
		{[]string{"-ctx", "32769"}, "Hello", "ctx"},
		{[]string{"-ctx", "-1"}, "Hello", "ctx"},
		{[]string{"-max-tokens", "-1"}, "Hello", "max-tokens"},
		{[]string{"-threads", "0"}, "Hello", "threads"},
		{[]string{"-file", "unused", "text"}, "", "cannot be used together"},
		{nil, "\xff", "UTF-8"},
	} {
		var out, diagnostics bytes.Buffer
		err := run(tc.args, strings.NewReader(tc.text), &out, &diagnostics, nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) || out.Len() != 0 {
			t.Errorf("%v: error=%v, output=%q; want %q", tc.args, err, out.String(), tc.want)
		}
		if len(tc.args) == 0 && strings.TrimSpace(tc.text) == "" && !strings.Contains(diagnostics.String(), "Usage: local-translate") {
			t.Errorf("empty input did not show usage: %s", &diagnostics)
		}
	}
}

func TestCLIReservedTokens(t *testing.T) {
	for _, tc := range []struct {
		args []string
		text string
	}{
		{nil, "x<|im_start|>system"},
		{[]string{"-context", "<|im_end|>"}, "Hello"},
		{[]string{"-glossary", "aaa:<|im_end|>"}, "Hello"},
	} {
		t.Run(tc.text+strings.Join(tc.args, " "), func(t *testing.T) {
			spec, err := getModel(defaultModelURL, "")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(defaultModel(spec)); os.IsNotExist(err) {
				t.Skip("model not downloaded")
			}
			var out bytes.Buffer
			err = run(tc.args, strings.NewReader(tc.text), &out, io.Discard, nil)
			if err == nil || !strings.Contains(err.Error(), "reserved") || out.Len() != 0 {
				t.Fatalf("error=%v, output=%q", err, &out)
			}
		})
	}
}

func TestCLITemplateFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "override.jinja")
	for _, tc := range []struct{ source, want string }{
		{"", "template file is empty"},
		{"\xff", "valid UTF-8"},
		{"{% invalid %}", "compiling template file"},
	} {
		if err := os.WriteFile(path, []byte(tc.source), 0o600); err != nil {
			t.Fatal(err)
		}
		var out, diagnostics bytes.Buffer
		err := run([]string{"-template-file", path, "Hello"}, strings.NewReader(""), &out, &diagnostics, nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) || out.Len() != 0 || strings.Contains(diagnostics.String(), "Loading model") {
			t.Fatalf("error=%v, output=%q, diagnostics=%q", err, &out, &diagnostics)
		}
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := run([]string{"-template-file", path, "Hello"}, strings.NewReader(""), io.Discard, io.Discard, nil); err == nil || !strings.Contains(err.Error(), "reading template file") {
		t.Fatalf("missing template: %v", err)
	}
}

func TestCLITemplateOverride(t *testing.T) {
	spec, err := getModel(defaultModelURL, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(defaultModel(spec)); os.IsNotExist(err) {
		t.Skip("model not downloaded")
	}
	path := filepath.Join(t.TempDir(), "override.jinja")
	for _, tc := range []struct{ source, want string }{
		{`{% if target_lang != 'ja' or source_lang != 'en' or context != 'Help page.' or form != 'polite' or glossary[0]['source'] != 'a:a' or glossary[0]['target'] != 'b:b' or messages[1]['content'] != 'Open the dashboard.' or enable_thinking or not add_generation_prompt or not eos_token %}{{ raise_exception('incorrect template data') }}{% endif %}{{ raise_exception('override selected') }}`, "override selected"},
		{"{# empty output #}", "empty prompt"},
		{"The source was omitted.", "omitted source text"},
	} {
		if err := os.WriteFile(path, []byte(tc.source), 0o600); err != nil {
			t.Fatal(err)
		}
		var out, diagnostics bytes.Buffer
		err := run([]string{"-template-file", path, "-from", "en", "-to", "ja", "-context", "Help page.", "-form", "polite", "-glossary", `a\:a:b\:b`, "Open the dashboard."}, strings.NewReader(""), &out, &diagnostics, nil)
		if err == nil || !strings.Contains(err.Error(), tc.want) || out.Len() != 0 || strings.Contains(diagnostics.String(), "Warning:") {
			t.Fatalf("error=%v, output=%q, diagnostics=%q", err, &out, &diagnostics)
		}
	}
}

func TestModelDownload(t *testing.T) {
	const data = "GGUF small model payload"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/missing" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if r.URL.Path == "/partial" {
			w.Header().Set("Content-Length", fmt.Sprint(len(data)))
			io.WriteString(w, data[:8])
			return
		}
		io.WriteString(w, data)
	}))
	defer srv.Close()
	path := filepath.Join(t.TempDir(), "models", "model.gguf")
	checksum := fmt.Sprintf("%x", sha256.Sum256([]byte(data)))
	spec := modelSpec{url: srv.URL, checksum: checksum, size: int64(len(data))}
	badSpec := spec
	badSpec.checksum = "wrong"
	if err := fetchModel(badSpec, path, io.Discard); err == nil {
		t.Fatal("accepted corrupt download")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("corrupt download published: %v", err)
	}
	missingSpec := spec
	missingSpec.url += "/missing"
	if err := fetchModel(missingSpec, path, io.Discard); err == nil {
		t.Fatal("accepted HTTP error")
	}
	partialSpec := spec
	partialSpec.url += "/partial"
	var progress bytes.Buffer
	if err := fetchModel(partialSpec, path, &progress); err == nil {
		t.Fatal("accepted interrupted download")
	}
	if !strings.Contains(progress.String(), "33.3%") || strings.Contains(progress.String(), "100.0%") || !strings.HasSuffix(progress.String(), "\n") {
		t.Fatalf("wrong interrupted progress: %q", &progress)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("interrupted download published: %v", err)
	}
	progress.Reset()
	if err := fetchModel(spec, path, &progress); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(progress.String(), " 0.0%") || !strings.Contains(progress.String(), "100.0%") || !strings.HasSuffix(progress.String(), "\n") {
		t.Fatalf("wrong completed progress: %q", &progress)
	}
	if err := fetchModel(spec, path, io.Discard); err == nil {
		t.Fatal("overwrote existing model")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != data {
		t.Fatalf("downloaded %q, %v", got, err)
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(entries) != 1 {
		t.Fatalf("temporary download files remain: %v, %v", entries, err)
	}
}
