import json
import pathlib

here = pathlib.Path(__file__).parent

CODING_PROMPT = """You are an expert Go engineer. Below is a description of a small HTTP service. Write the complete Go implementation.

The service is called "ledgerd". It must:
1. Expose a REST API on port 8080 with endpoints: POST /accounts, GET /accounts/{id}, POST /accounts/{id}/transactions, GET /accounts/{id}/transactions.
2. Store accounts and transactions in memory using a sync.RWMutex-protected map, keyed by a UUID string.
3. Each account has: ID, Owner (string), Balance (int64, cents), CreatedAt (time.Time).
4. Each transaction has: ID, AccountID, Amount (int64, cents, can be negative), Description (string), CreatedAt (time.Time).
5. Posting a transaction must atomically update the account balance and must reject the transaction with HTTP 422 if the resulting balance would go negative.
6. All handlers must validate JSON bodies and return HTTP 400 with a JSON error body on malformed input.
7. Include a graceful shutdown handler that listens for SIGINT/SIGTERM and drains in-flight requests with a 5 second timeout using http.Server.Shutdown.
8. Include a /healthz endpoint returning 200 OK with body "ok".
9. Use only the Go standard library (net/http, encoding/json, sync, time, os, os/signal, context, log) plus github.com/google/uuid for ID generation.
10. Structure the code as a single main.go file with clear separation between the storage layer, the HTTP handlers, and main().

Write idiomatic Go with proper error handling. Do not use any web framework. Return only the code in a single fenced go code block, no other commentary.
"""

GO_FILE = """package ledger

import (
\t"errors"
\t"sync"
\t"time"
)

var ErrInsufficientFunds = errors.New("insufficient funds")
var ErrAccountNotFound = errors.New("account not found")

type Account struct {
\tID        string
\tOwner     string
\tBalance   int64
\tCreatedAt time.Time
}

type Transaction struct {
\tID          string
\tAccountID   string
\tAmount      int64
\tDescription string
\tCreatedAt   time.Time
}

type Store struct {
\tmu           sync.RWMutex
\taccounts     map[string]*Account
\ttransactions map[string][]*Transaction
}

func NewStore() *Store {
\treturn &Store{
\t\taccounts:     make(map[string]*Account),
\t\ttransactions: make(map[string][]*Transaction),
\t}
}

func (s *Store) CreateAccount(owner string) *Account {
\ts.mu.Lock()
\tdefer s.mu.Unlock()
\tacc := &Account{
\t\tID:        newID(),
\t\tOwner:     owner,
\t\tBalance:   0,
\t\tCreatedAt: time.Now(),
\t}
\ts.accounts[acc.ID] = acc
\treturn acc
}

func (s *Store) GetAccount(id string) (*Account, error) {
\ts.mu.RLock()
\tdefer s.mu.RUnlock()
\tacc, ok := s.accounts[id]
\tif !ok {
\t\treturn nil, ErrAccountNotFound
\t}
\treturn acc, nil
}

// PostTransaction is deliberately naive: it does not check for overdraft.
// TODO: enforce ErrInsufficientFunds before applying a debit.
func (s *Store) PostTransaction(accountID string, amount int64, desc string) (*Transaction, error) {
\ts.mu.Lock()
\tdefer s.mu.Unlock()
\tacc, ok := s.accounts[accountID]
\tif !ok {
\t\treturn nil, ErrAccountNotFound
\t}
\tacc.Balance += amount
\ttx := &Transaction{
\t\tID:          newID(),
\t\tAccountID:   accountID,
\t\tAmount:      amount,
\t\tDescription: desc,
\t\tCreatedAt:   time.Now(),
\t}
\ts.transactions[accountID] = append(s.transactions[accountID], tx)
\treturn tx, nil
}

func newID() string {
\treturn time.Now().Format("20060102150405.000000000")
}
"""

EDIT_PROMPT = f"""Here is a Go file:

```go
{GO_FILE}
```

Change only the `PostTransaction` function so that it rejects a transaction with `ErrInsufficientFunds` if `acc.Balance + amount < 0`, before mutating the balance. Keep everything else in the file byte-for-byte identical, including comments, imports, and formatting. Return the complete file back in a single fenced go code block, no commentary.
"""

def make_prefix(n_tokens_approx=3000):
    # ~1.3 tokens/word for code-like text; build a synthetic but stable "codebase context" doc
    lines = []
    lines.append("You are a senior Go reviewer with full context on the `ledgerd` repository. "
                  "Below is the full context bundle: repository conventions, prior decisions, and open files. "
                  "Read it once; it will not change between turns.\n")
    lines.append("## Repository conventions\n")
    conventions = [
        "All errors are wrapped with fmt.Errorf and %w.",
        "No third-party web framework; net/http only.",
        "All exported types have a one-line doc comment.",
        "Table-driven tests live beside the code they test.",
        "Context is always the first parameter of any function that can block.",
        "Logging goes through the shared *slog.Logger, never the log package directly.",
        "All monetary values are int64 cents, never float64.",
        "Mutexes guard a single struct's fields, never span multiple structs.",
        "HTTP handlers translate errors to status codes in one place: errToStatus().",
        "Every goroutine started outside a request must be tracked by a sync.WaitGroup.",
    ]
    for i in range(24):
        lines.append(f"{i+1}. {conventions[i % len(conventions)]} (rule id CONV-{i+1:03d})\n")

    lines.append("\n## Prior decisions (ADR log)\n")
    for i in range(40):
        lines.append(
            f"ADR-{i+1:03d}: Decided to keep account storage in-memory behind an interface `AccountStore` "
            f"so a future Postgres-backed implementation can be swapped in without touching handlers. "
            f"Rejected alternative: embedding SQLite directly in v0, revisit after v0.3 (owner: platform team, "
            f"tracking issue LEDGER-{100+i}).\n"
        )

    lines.append("\n## Open files in the editor\n")
    lines.append("### file: internal/store/store.go\n```go\n" + GO_FILE + "```\n")

    lines.append("\n## Tool results so far (this session)\n")
    for i in range(6):
        lines.append(
            f"[tool_result #{i+1}] ran `go vet ./...` on internal/store: 0 issues found. "
            f"ran `go test ./internal/store/... -run TestPostTransaction` at commit abc{i}def: PASS (3 subtests, 12ms).\n"
        )

    text = "".join(lines)
    return text

PREFIX = make_prefix()

SUFFIXES = [
    "\n\nGiven the above, does `PostTransaction` currently allow the balance to go negative? Answer yes or no and name the exact line.",
    "\n\nGiven the above, list the CONV rule numbers that `PostTransaction` currently violates, if any.",
    "\n\nGiven the above, write a one-sentence commit message for adding the overdraft check to `PostTransaction`.",
]

TRIM_MARKER = "[tool_result #3]"

def trimmed_prefix():
    lines = PREFIX.split("\n")
    out = [l for l in lines if TRIM_MARKER not in l]
    return "\n".join(out)

if __name__ == "__main__":
    (here / "coding_prompt.txt").write_text(CODING_PROMPT)
    (here / "edit_prompt.txt").write_text(EDIT_PROMPT)
    (here / "prefix.txt").write_text(PREFIX)
    (here / "prefix_trimmed.txt").write_text(trimmed_prefix())
    for i, s in enumerate(SUFFIXES):
        (here / f"suffix_{i+1}.txt").write_text(s)
    print("coding_prompt chars:", len(CODING_PROMPT))
    print("edit_prompt chars:", len(EDIT_PROMPT))
    print("prefix chars:", len(PREFIX))
    print("trimmed prefix chars:", len(trimmed_prefix()))
