// Command gendata posts N fake heartbeats to a gakatime instance's bulk endpoint
// (port of hakatime tools/Main.hs). Used for local verification.
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"math/rand"
	"net/http"
	"os"
	"time"
)

// heartbeat is the wire shape expected by /heartbeats.bulk (convertReservedWords).
type heartbeat struct {
	Sender    *string `json:"sender"`
	Editor    *string `json:"editor"`
	UserAgent string  `json:"user_agent"`
	Branch    *string `json:"branch"`
	Language  *string `json:"language"`
	Project   *string `json:"project"`
	Type      string  `json:"type"`
	Machine   *string `json:"machine"`
	TimeSent  float64 `json:"time"`
	Entity    string  `json:"entity"`
}

var (
	languages = []string{"C++", "Docker", "Go", "Haskell", "JSON", "JavaScript", "Nix", "Python", "Ruby", "TypeScript", "YAML"}
	projects  = []string{"hakatime", "my-app", "bootstrap", "jQuery", "Kubernetes", "nixpkgs", "zulip", "Pandas"}
	files     = []string{"src/app", "main", "db/test_runner", "data/fixtures", "src/controller", "main/component",
		"pkg/adapter/main", "src/init", "keys", "src/spinner", "ui/scrollbar", "src/ui/test", "src/ui/button",
		"api/v1/service", "api/v1/volume", "api/v1/metadata", "api/v1/db", "package", "test", "utils", "commons",
		"manager", "config", "resources"}
	exts   = []string{".cpp", ".go", ".hs", ".json", ".js", ".ts", ".py", ".rb", ".yaml", ".rs"}
	agents = []string{
		"wakatime/1.0 (Linux-5.4) go1.20 vscode/1.70 vscode-wakatime/4.0",
		"wakatime/1.0 (Darwin-22.1) python3.9 vim/9.0 vim-wakatime/9.0",
		"wakatime/1.0 (Windows-10) node16 intellij/2022 intellij-wakatime/13.0",
	}
)

func ptr(s string) *string { return &s }

func generateTimeline() []heartbeat {
	steps := rand.Intn(71) + 10 // 10..80
	lang := languages[rand.Intn(len(languages))]
	proj := projects[rand.Intn(len(projects))]
	file := files[rand.Intn(len(files))] + exts[rand.Intn(len(exts))]
	ua := agents[rand.Intn(len(agents))]
	// Random start within the last 60 days.
	start := time.Now().Add(-time.Duration(rand.Intn(60*24)) * time.Hour)

	out := make([]heartbeat, 0, steps)
	t := start
	for i := 0; i < steps; i++ {
		out = append(out, heartbeat{
			Sender:    ptr("demo"),
			Editor:    ptr("vim"),
			UserAgent: ua,
			Branch:    ptr("master"),
			Language:  ptr(lang),
			Project:   ptr(proj),
			Type:      "file",
			Machine:   ptr("laptop"),
			TimeSent:  float64(t.Unix()),
			Entity:    file,
		})
		t = t.Add(120 * time.Second) // ~2 min spacing
	}
	return out
}

func main() {
	url := flag.String("url", "localhost", "Endpoint host")
	port := flag.Int("port", 8080, "Port")
	token := flag.String("token", "", "Raw UUID API token (required)")
	num := flag.Int("num", 100, "Number of timelines to send")
	flag.Parse()

	if *token == "" {
		fmt.Fprintln(os.Stderr, "--token is required")
		os.Exit(1)
	}

	var all []heartbeat
	for i := 0; i < *num; i++ {
		all = append(all, generateTimeline()...)
	}

	body, err := json.Marshal(all)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	scheme := "http"
	if *port == 443 {
		scheme = "https"
	}
	endpoint := fmt.Sprintf("%s://%s:%d/api/v1/users/current/heartbeats.bulk", scheme, *url, *port)

	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	// Auth: Basic base64(uuid) — the server base64-encodes... no: the stored token
	// is base64(uuid) and the client sends Basic base64(uuid). ParseAuthHeader strips
	// "Basic " and compares directly, so we must send base64(raw uuid).
	req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte(*token)))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Machine-Name", "laptop")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		fmt.Fprintf(os.Stderr, "server returned %d\n", resp.StatusCode)
		os.Exit(1)
	}
	fmt.Printf("Heartbeats sent successfully! (%d heartbeats)\n", len(all))
}
