package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/sasuke39/open-warp/internal/agentruntime"
	"github.com/sasuke39/open-warp/internal/config"
	pb "github.com/sasuke39/open-warp/internal/proto"
	"google.golang.org/protobuf/proto"
)

// contractAgent is deliberately user-level: scenarios do not know whether the
// implementation is the in-process Native loop or an NDJSON Sidecar. A new
// built-in framework must provide this adapter and inherits every scenario.
type contractAgent interface {
	Turn(context.Context, string, string) contractObservation
	Close(context.Context) error
}

type contractObservation struct {
	Text      string
	Completed bool
	Failed    string
}

type contractFactory func(*testing.T, *contractProvider) contractAgent

var contractFactories = map[string]contractFactory{
	"native":           newNativeContractAgent,
	"pi-agent":         newPiContractAgent,
	"deepseek-harness": newDSHContractAgent,
}

func TestEveryBundledAgentHasContractRunner(t *testing.T) {
	want := append([]string(nil), config.BundledAgentDrivers...)
	got := make([]string, 0, len(contractFactories))
	for name := range contractFactories {
		got = append(got, name)
	}
	sort.Strings(want)
	sort.Strings(got)
	if strings.Join(want, ",") != strings.Join(got, ",") {
		t.Fatalf("bundled Agent drivers=%v, contract runners=%v; every new driver must register a real contract runner", want, got)
	}
}

func TestAgentContractMatrix(t *testing.T) {
	for _, driverName := range config.BundledAgentDrivers {
		factory := contractFactories[driverName]
		t.Run(driverName, func(t *testing.T) {
			provider := newContractProvider(t)
			agent := factory(t, provider)
			t.Cleanup(func() {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := agent.Close(ctx); err != nil {
					t.Errorf("close %s contract runner: %v", driverName, err)
				}
			})
			runSharedAgentContract(t, agent)
		})
	}
}

func runSharedAgentContract(t *testing.T, agent contractAgent) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("plain turn completes", func(t *testing.T) {
		result := agent.Turn(ctx, "plain-conversation", "CONTRACT_PLAIN")
		assertContractResult(t, result, "CONTRACT_OK")
	})

	t.Run("same conversation keeps memory", func(t *testing.T) {
		first := agent.Turn(ctx, "memory-conversation", "CONTRACT_REMEMBER WARP-7319")
		assertContractResult(t, first, "REMEMBERED")
		second := agent.Turn(ctx, "memory-conversation", "CONTRACT_RECALL")
		assertContractResult(t, second, "WARP-7319")
	})

	t.Run("different conversations stay isolated", func(t *testing.T) {
		result := agent.Turn(ctx, "isolated-conversation", "CONTRACT_RECALL")
		assertContractResult(t, result, "UNKNOWN")
		if strings.Contains(result.Text, "WARP-7319") {
			t.Fatalf("conversation leaked memory: %q", result.Text)
		}
	})

	t.Run("a later turn works after completion", func(t *testing.T) {
		result := agent.Turn(ctx, "plain-conversation", "CONTRACT_NEXT")
		assertContractResult(t, result, "NEXT_TURN_OK")
	})
}

func assertContractResult(t *testing.T, result contractObservation, contains string) {
	t.Helper()
	if result.Failed != "" {
		t.Fatalf("Turn failed: %s", result.Failed)
	}
	if !result.Completed {
		t.Fatal("Turn did not reach a completed state")
	}
	if !strings.Contains(result.Text, contains) {
		t.Fatalf("assistant text %q does not contain %q", result.Text, contains)
	}
}

type contractProvider struct {
	server *httptest.Server
}

func newContractProvider(t *testing.T) *contractProvider {
	t.Helper()
	provider := &contractProvider{}
	provider.server = httptest.NewServer(http.HandlerFunc(provider.serveHTTP))
	t.Cleanup(provider.server.Close)
	return provider
}

func (p *contractProvider) serveHTTP(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	text := contractResponseText(body)
	w.Header().Set("Content-Type", "text/event-stream")
	w.WriteHeader(http.StatusOK)
	chunk := map[string]any{
		"id": "contract", "object": "chat.completion.chunk", "created": 1, "model": "contract-model",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"role": "assistant", "content": text}, "finish_reason": nil}},
	}
	finish := map[string]any{
		"id": "contract", "object": "chat.completion.chunk", "created": 1, "model": "contract-model",
		"choices": []any{map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
		"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 3, "total_tokens": 13},
	}
	encodedChunk, _ := json.Marshal(chunk)
	encodedFinish, _ := json.Marshal(finish)
	_, _ = fmt.Fprintf(w, "data: %s\n\ndata: %s\n\ndata: [DONE]\n\n", encodedChunk, encodedFinish)
}

func contractResponseText(body []byte) string {
	content := string(body)
	switch {
	case strings.Contains(content, "CONTRACT_RECALL"):
		if strings.Contains(content, "REMEMBERED WARP-7319") {
			return "WARP-7319"
		}
		return "UNKNOWN"
	case strings.Contains(content, "CONTRACT_REMEMBER"):
		return "REMEMBERED WARP-7319"
	case strings.Contains(content, "CONTRACT_NEXT"):
		return "NEXT_TURN_OK"
	default:
		return "CONTRACT_OK"
	}
}

type nativeContractAgent struct {
	server *Server
	t      *testing.T
	nextID int
}

func newNativeContractAgent(t *testing.T, provider *contractProvider) contractAgent {
	t.Helper()
	disabled := false
	cfg := &config.Config{
		Provider: "openai", BaseURL: provider.server.URL + "/v1", APIKey: "contract-key", Model: "contract-model",
		AgentRuntime: config.RuntimeConfig{Driver: "native"},
		Memory:       config.MemoryConfig{Enabled: &disabled},
	}
	return &nativeContractAgent{server: NewServer(cfg, filepath.Join(t.TempDir(), "config.yaml")), t: t}
}

func (a *nativeContractAgent) Turn(ctx context.Context, conversationID, promptText string) contractObservation {
	a.nextID++
	taskID := fmt.Sprintf("native-contract-task-%d", a.nextID)
	request := &pb.Request{
		TaskContext: &pb.Request_TaskContext{Tasks: []*pb.Task{{Id: taskID}}},
		Input: &pb.Request_Input{Type: &pb.Request_Input_UserQuery_{
			UserQuery: &pb.Request_Input_UserQuery{Query: promptText},
		}},
		Metadata: &pb.Request_Metadata{ConversationId: conversationID},
	}
	raw, err := proto.Marshal(request)
	if err != nil {
		return contractObservation{Failed: err.Error()}
	}
	recorder := httptest.NewRecorder()
	httpRequest := httptest.NewRequest(http.MethodPost, "/ai/multi-agent", bytes.NewReader(raw)).WithContext(ctx)
	a.server.handleAgentRequest(recorder, httpRequest)
	observation := contractObservation{}
	for _, event := range decodeResponseEvents(a.t, recorder.Body.String()) {
		if finished := event.GetFinished(); finished != nil {
			observation.Completed = finished.GetDone() != nil
			if finishError := finished.GetInternalError(); finishError != nil {
				observation.Failed = finishError.GetMessage()
			}
		}
		for _, action := range event.GetClientActions().GetActions() {
			for _, message := range action.GetAddMessagesToTask().GetMessages() {
				observation.Text += message.GetAgentOutput().GetText()
			}
		}
	}
	return observation
}

func (a *nativeContractAgent) Close(ctx context.Context) error { return a.server.closeBackground(ctx) }

type processContractAgent struct {
	driver agentruntime.Driver
	nextID int
}

func newPiContractAgent(t *testing.T, provider *contractProvider) contractAgent {
	return newProcessContractAgent(t, "pi-agent", provider.server.URL+"/v1")
}

func newDSHContractAgent(t *testing.T, provider *contractProvider) contractAgent {
	return newProcessContractAgent(t, "deepseek-harness", provider.server.URL)
}

func newProcessContractAgent(t *testing.T, driverName, baseURL string) contractAgent {
	t.Helper()
	node := contractNode(t)
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	runtimeDir := "pi-agent"
	if driverName == "deepseek-harness" {
		runtimeDir = "deepseek-harness"
	}
	entry := filepath.Join(repoRoot, "integrations", runtimeDir, "dist", "main.js")
	if _, err := os.Stat(entry); err != nil {
		t.Fatalf("%s runtime is not built at %s; run npm build before the contract matrix", driverName, entry)
	}
	env := append(os.Environ(),
		"AGENT_RUNTIME_API_KEY=contract-key",
		"AGENT_RUNTIME_BASE_URL="+baseURL,
		"AGENT_RUNTIME_MODEL=contract-model",
		"AGENT_RUNTIME_THINKING_DISABLED=true",
		"DEEPSEEK_API_KEY=contract-key",
		"DEEPSEEK_BASE_URL="+baseURL,
		"DSH_MODEL=contract-model",
		"DSH_PROVIDER=deepseek-official",
		"DSH_THINKING_DISABLED=true",
		"PI_AGENT_DIR="+t.TempDir(),
		"PI_SESSION_ROOT="+t.TempDir(),
		"DSH_SESSION_ROOT="+t.TempDir(),
	)
	driver, err := agentruntime.NewProcessDriver(agentruntime.ProcessConfig{
		Name: driverName, Command: node, Args: []string{entry}, Env: env,
	})
	if err != nil {
		t.Fatal(err)
	}
	return &processContractAgent{driver: driver}
}

func (a *processContractAgent) Turn(ctx context.Context, conversationID, promptText string) contractObservation {
	a.nextID++
	request := agentruntime.TurnRequest{
		ConversationID: conversationID,
		TurnID:         fmt.Sprintf("turn-%d", a.nextID),
		TaskID:         fmt.Sprintf("task-%d", a.nextID),
		RequestID:      fmt.Sprintf("request-%d", a.nextID),
		WorkingDir:     os.TempDir(),
		Inputs:         []agentruntime.Input{{Kind: agentruntime.InputUserMessage, Content: promptText}},
	}
	result := contractObservation{}
	err := a.driver.Exchange(ctx, request, func(event agentruntime.Event) error {
		switch event.Type {
		case agentruntime.EventAssistantDelta, agentruntime.EventAssistantFinal:
			result.Text += event.Text
		case agentruntime.EventTurnCompleted:
			result.Completed = true
		case agentruntime.EventTurnFailed:
			result.Failed = event.Error
		}
		return nil
	})
	if err != nil && result.Failed == "" {
		result.Failed = err.Error()
	}
	return result
}

func (a *processContractAgent) Close(ctx context.Context) error { return a.driver.Close(ctx) }

func contractNode(t *testing.T) string {
	t.Helper()
	candidates := []string{
		os.Getenv("WARPLOCAL_CONTRACT_NODE"),
		"/Applications/OpenWarp.app/Contents/Helpers/node-runtime",
		"/Applications/WarpLocal.app/Contents/Helpers/node-runtime",
	}
	if path, err := exec.LookPath("node"); err == nil {
		candidates = append(candidates, path)
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate) == "" {
			continue
		}
		output, err := exec.Command(candidate, "-p", "process.versions.node").Output()
		if err != nil {
			continue
		}
		parts := strings.Split(strings.TrimSpace(string(output)), ".")
		major, _ := strconv.Atoi(parts[0])
		minor := 0
		if len(parts) > 1 {
			minor, _ = strconv.Atoi(parts[1])
		}
		if major >= 24 || (major == 22 && minor >= 19) {
			return candidate
		}
	}
	t.Fatalf("Agent contract matrix requires Node ^22.19 or >=24 on %s; set WARPLOCAL_CONTRACT_NODE", runtime.GOOS)
	return ""
}

var _ contractAgent = (*nativeContractAgent)(nil)
var _ contractAgent = (*processContractAgent)(nil)
