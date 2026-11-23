package decide

import (
	"context"
	"errors"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/kyleking/wavez/internal/config"
)

// The variables of the user's own environment that say where a prompt and
// a key may go. A project's .wavez.toml is checked in, so it is whoever
// wrote the repository speaking, and it alone can only name a model server
// on this machine.
const (
	// EndpointEnv names the endpoint the user trusts. When set it is the
	// endpoint asked, whatever the project configured, and it is the only
	// endpoint the API key is sent to.
	EndpointEnv = "WAVEZ_DECIDE_ENDPOINT"
	// APIKeyEnv holds the optional API key for the endpoint EndpointEnv
	// names. A local model server needs none.
	APIKeyEnv = "WAVEZ_DECIDE_API_KEY" //nolint:gosec // the name of a variable, not a credential
)

// Where a project's decision log lives, relative to its root.
const (
	stateDir = ".wavez"
	logFile  = "decisions.jsonl"
)

// Errors a configured backend that may not be asked answers with. Each is
// a fallback's reason and nothing an agent sees.
var (
	// ErrEndpointNotTrusted is the answer of a project-configured endpoint
	// that is not on this machine and that EndpointEnv does not name.
	ErrEndpointNotTrusted = errors.New("the project's endpoint is not loopback and " + EndpointEnv + " is not set")
	// ErrNoModel is the answer of an endpoint with no model configured.
	ErrNoModel = errors.New("no model is configured for the endpoint")
)

// Configured returns the Engine a project's configuration and the user's
// environment describe. It keeps no log, so asking by hand or evaluating a
// corpus leaves the decision log to the hook.
//
// With no endpoint it has no backend, and every question gets its rule or
// its fallback. The endpoint is EndpointEnv when the user set it, and the
// project's otherwise. A project's endpoint is asked only when its host is
// loopback, and never with the API key: a cloned repository cannot point a
// person's prompts or their key at a host of its choosing. Any other
// project endpoint falls back with ErrEndpointNotTrusted as the reason.
func Configured(cfg config.Decide) *Engine {
	return New(backendOptions(cfg, os.Getenv)...)
}

// FromConfig is Configured with every decision appended to the decision
// log under root's .wavez directory. It is what the hook runs.
func FromConfig(root string, cfg config.Decide) *Engine {
	opts := backendOptions(cfg, os.Getenv)

	return New(append(opts, WithLog(filepath.Join(root, stateDir, logFile)))...)
}

func backendOptions(cfg config.Decide, getenv func(string) string) []Option {
	endpoint, key := strings.TrimSpace(getenv(EndpointEnv)), ""

	switch {
	case endpoint != "":
		key = getenv(APIKeyEnv)
	case cfg.Endpoint == "":
		return nil
	case !loopback(cfg.Endpoint):
		return []Option{WithBackend(refused{err: ErrEndpointNotTrusted})}
	default:
		endpoint = cfg.Endpoint
	}

	if cfg.Model == "" {
		return []Option{WithBackend(refused{err: ErrNoModel})}
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = config.DefaultDecideTimeout
	}

	return []Option{WithBackend(NewOpenAI(endpoint, cfg.Model, timeout, WithAPIKey(key)))}
}

// loopback reports whether endpoint's host is this machine: the name
// localhost or a loopback address.
func loopback(endpoint string) bool {
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return false
	}

	host := parsed.Hostname()
	if strings.EqualFold(host, "localhost") {
		return true
	}

	ip := net.ParseIP(host)

	return ip != nil && ip.IsLoopback()
}

// refused is the backend of a configuration that may not be asked. It
// sends nothing and answers with why, which becomes the fallback's reason.
type refused struct {
	err error
}

func (refused) Name() string { return "refused" }

func (r refused) Answer(context.Context, Question, string) (Answer, error) {
	return Answer{}, r.err
}
