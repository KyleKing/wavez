# Vector + AST Code Search Implementation Plan

> Goal: Deliver an offline-first, hybrid (structural + semantic) code search system backed by DuckDB, leveraging AST/structural pattern engines (tree-sitter + Semgrep/ast-grep) and local embedding models (small LLMs) with seamless Neovim Telescope integration and an API surface consumable by AI coding assistants.

---
## 1. Objectives & Non-Goals
- **Primary Objectives**:
  - Fast, accurate retrieval for: natural language intent, structural patterns, refactors, and semantic similarity.
  - Fully local / air‑gapped operation (privacy-first) with incremental indexing.
  - Extensible retrieval pipeline (AST, tokens, embeddings, hybrid ranking).
  - Tight Neovim Telescope UX (sub‑second perceived latency for common queries).
  - Provide a clean programmatic interface (CLI + local HTTP/gRPC) for AI assistants & RAG pipelines.
- **Secondary Objectives**:
  - Support multi-repo workspaces; handle mono‑repos (100k–2M LOC) efficiently.
  - Enable future enrichment: call graph, data flow, clone clustering, symbol graph.
- **Non-Goals (Initial Phases)**:
  - Cross-language type resolution or deep inter-procedural data flow.
  - Large-scale distributed index sharding (focus: single developer workstation).
  - Training custom embedding models (reuse existing open-source models initially).

---
## 2. Personas & Use Cases
- **Individual Developer**: “Where is JWT validation implemented?”
- **Refactorer**: “Replace all deprecated API constructions matching structural pattern X except in tests.”
- **Security Reviewer**: “List dynamic SQL construction patterns; show suspicious concatenations.”
- **AI Assistant Backend**: Convert NL query → ranked code snippets + structured context windows.
- **Toolsmith**: Batch generate summaries / embeddings / symbol maps for new repository.

---
## 3. High-Level Architecture
```
               ┌────────────────────────┐
               │   Repo Watcher / CLI   │
               └──────────┬─────────────┘
                          │ file events
                Ingestion / Change Detection
                          │
        ┌─────────────────▼─────────────────┐
        │    Normalization & Metadata       │
        │  (path, lang, git rev, hashes)    │
        └──────────┬───────────┬───────────┘
                   │           │
              ┌────▼───┐  ┌───▼──────┐
              │ AST     │  │ Chunker  │  (logical code + sliding windows)
              │ (tree-  │  │ + Token  │
              │ sitter) │  │ Stats    │
              └────┬────┘  └───┬──────┘
                   │ AST nodes │ code chunks
                   ▼           ▼
         ┌──────────────────────────────┐
         │  Embedding Generation Layer  │ (local model / batching / caching)
         └────────────────┬─────────────┘
                          ▼
                    DuckDB Storage
              (relational + vector (VSS ext))
                          │
                    Query Orchestrator
        (NL intent → plan → retrieval stages → rank)
                          │
         ┌────────────────┴──────────────────┐
         │  Neovim Telescope  │  Assistant API│
         └───────────────────────────────────┘
```

---
## 4. Core Components Overview
- **Repo Scanner & Watcher**: Initial full scan + incremental (mtime + content hash + git diff fallback).
- **Language Detection**: Use file extension + heuristic fallback; map to tree-sitter grammar / Semgrep language IDs.
- **AST Extraction**: tree-sitter for speed + uniform node interface; option to run Semgrep patterns separately.
- **Chunk Builder**: Multiple granularities: function/symbol-level, logical blocks, sliding window (e.g., 40–80 LOC) for semantic recall.
- **Embedding Layer**: Batching + dedupe via (content_hash → embedding cache). Pluggable model registry.
- **DuckDB Storage**: Unified analytical + vector similarity store via VSS extension; simplifies zero-dependency deployment.
- **Query Orchestrator**: Strategy selection (keyword / structural / semantic / hybrid) + multi-stage rerank.
- **Ranking Layer**: Combines vector similarity, structural match confidence, recency, file popularity, symbol specificity.
- **Telemetry (Local Only)**: Latency, cache hit rate, embedding batch size efficiency, index freshness.

---
## 5. DuckDB Data Model (Initial)
Extensions: `INSTALL vss; LOAD vss;`

```sql
-- Files & metadata
CREATE TABLE files (
  file_id         BIGINT PRIMARY KEY,
  path            TEXT NOT NULL,
  language        TEXT NOT NULL,
  rel_path_hash   BLOB,            -- hash(path)
  content_hash    BLOB,            -- hash(full file content)
  loc             INTEGER,
  byte_size       INTEGER,
  repo_root       TEXT,
  git_commit      TEXT,
  last_indexed_at TIMESTAMP
);

-- Symbols / functions / classes
CREATE TABLE symbols (
  symbol_id       BIGINT PRIMARY KEY,
  file_id         BIGINT REFERENCES files(file_id),
  name            TEXT,
  kind            TEXT,            -- function, class, method, var
  start_line      INTEGER,
  end_line        INTEGER,
  signature       TEXT,
  ast_serialized  TEXT,            -- compressed or hash-key to side table
  stable_symbol_hash BLOB          -- hash(name+signature+path)
);

-- Code chunks for semantic search
CREATE TABLE chunks (
  chunk_id        BIGINT PRIMARY KEY,
  file_id         BIGINT REFERENCES files(file_id),
  symbol_id       BIGINT REFERENCES symbols(symbol_id),
  start_line      INTEGER,
  end_line        INTEGER,
  token_count     INTEGER,
  content         TEXT,
  content_hash    BLOB,
  embedding_id    BIGINT,          -- join to embeddings
  created_at      TIMESTAMP
);

-- Embeddings table (store once per unique content hash)
CREATE TABLE embeddings (
  embedding_id    BIGINT PRIMARY KEY,
  content_hash    BLOB UNIQUE,
  dims            INTEGER,
  vector          FLOAT[768],      -- actual size depends on model (DuckDB vss)
  model_name      TEXT,
  created_at      TIMESTAMP
);

-- VSS index (approximate NN)
CREATE INDEX chunks_embedding_index ON embeddings USING vss(vector) WITH (metric='cosine');

-- Structural pattern matches cache (optional precomp / warming)
CREATE TABLE structural_cache (
  pattern_key     TEXT,
  file_id         BIGINT,
  match_ranges    TEXT,            -- JSON array [[s,e],...]
  engine          TEXT,            -- 'semgrep' | 'tsquery'
  computed_at     TIMESTAMP,
  PRIMARY KEY(pattern_key, file_id)
);

-- Query logs (local, ephemeral) for tuning
CREATE TABLE query_log (
  query_id        BIGINT PRIMARY KEY,
  raw_query       TEXT,
  normalized_query TEXT,
  strategy        TEXT,
  latency_ms      INTEGER,
  result_ids      TEXT,
  created_at      TIMESTAMP
);
```

Optional future tables: `call_graph_edges`, `clone_clusters`.

---
## 6. Embedding Strategy
- **Model Selection (Initial)**:
  - `intfloat/e5-small-v2` (general-purpose, 384–768 dims) or `jinaai/jina-embeddings-v2-base-code` (code-aware) → quantize for CPU.
  - For stricter offline: GGUF quantized MiniLM or `nomic-embed-text-v1.5`.
- **Abstraction Level**: Use both symbol-level and window-level embeddings; maintain two retrieval pools.
- **Chunking Heuristics**:
  - Prefer semantic boundaries (function, class) else fallback sliding window (stride 50% overlap) to preserve recall.
  - Max tokens ~256–512 per embedding for small models.
- **Caching**: `content_hash -> embedding_id`; skip regen if unchanged.
- **Compression**: If memory pressure: product quantization (future) or store float16 once DuckDB supports; otherwise, gzip `vector` column is not beneficial (already dense) — rely on dims minimization.
- **Batching**: Aggregate up to N tokens (e.g., 8–12K) per inference batch to saturate CPU vectorization.

---
## 7. AST & Structural Layer
- **Primary Parser**: tree-sitter for universal incremental parse (provides edit-based reparse for fast updates).
- **Structural Engines**:
  - **Tree-sitter Queries (S-expr)** for lightweight structural matching.
  - **Semgrep** for higher-level pattern language + metavariables + taint mode (optional, slower).
  - **ast-grep** (optional alternative) if richer ergonomic pattern DSL desired.
- **Normalization**:
  - Extract: node type path, identifier names (optionally normalized), literal kind counts, depth stats.
  - Store small canonical forms (e.g., hashing identifier-stripped subtree) → supports clone detection & filtering.
- **Pattern Translation** (LLM-mediated): NL → (pattern type classification) → either TSQuery / Semgrep pattern skeleton with metavariables → refine by applying & verifying non-empty matches.

---
## 8. Hybrid Retrieval Algorithms
### 8.1 Strategy Selection
Decision features: query length, presence of code tokens (`[(){};]` density), natural language verbs, explicit pattern keywords (e.g., `pattern:`). Classifier (rule-based + optional small model) chooses: `STRUCTURAL | SEMANTIC | HYBRID | KEYWORD_ONLY`.

### 8.2 Pipelines
1. **Semantic-First Hybrid**:
   1. Embed query (or generate synthetic variants via short rewrite model).
   2. ANN search (k=200 combined across symbol + window pools).
   3. Structural refinement: Run quick tree-sitter filters (node type constraints) over candidate files.
   4. Optional Semgrep targeted patterns (if classifier suggests security/refactor intent).
   5. Rerank with weighted scoring.
2. **Structural-First Hybrid**:
   1. NL → structural pattern via LLM translator.
   2. Apply pattern engine; collect matches (M).
   3. For large M, sample subset to build ephemeral centroid embedding to refine semantic expansion.
   4. Expand semantically similar neighbors (embedding search) to catch near-misses.
3. **Pure Structural**: Direct TSQuery / Semgrep (fast path if user supplies explicit pattern).
4. **Pure Semantic**: ANN + rerank + metadata diversity (avoid top-K all from one file).

### 8.3 Scoring Function (example)
```
score = w_vec * cosine_sim
      + w_struct * struct_confidence
      + w_symbol * symbol_specificity
      + w_recency * recency_boost
      + w_pop * file_popularity_penalty
      - w_len * length_penalty
```
Weights tuned via offline evaluation (MRR, nDCG).

### 8.4 Pseudocode (Semantic-First)
```python
def hybrid_semantic_first(nl_query: str):
    intent = classify(nl_query)
    emb = embed(nl_query)
    candidates = ann_search(emb, k=200)
    enriched = []
    for c in candidates:
        ast_feats = quick_ast_filter(c)
        struct_conf = structural_score(ast_feats, intent)
        enriched.append(apply_features(c, struct_conf))
    reranked = rank(enriched)
    return reranked[:limit]
```

---
## 9. Telescope Integration (Neovim)
- **Commands / Pickers**:
  - `:CodeSearch <query>` → hybrid (auto strategy).
  - `:CodeStruct <pattern>` → direct structural (tsquery / Semgrep).
  - `:CodeSymbols <query>` → symbol-only vector search.
  - `:CodeRefactor <nl description>` → NL → structural pattern → preview matches.
- **Implementation Notes**:
  - Use async jobstart to call a local CLI (`codesearch query --json`).
  - Streaming results progressively: return first batch (~50) quickly, background rerank continues.
  - Highlight lines (`previewer`) with context window (±6 lines) and ephemeral inline symbol doc (cached summary if available).
  - Index refresh triggers on `BufWritePost` (debounced) → enqueue changed file.
- **Latency Targets**:
  - Warm semantic query: <300ms first batch.
  - Cold (model load): <2s once per session.
  - Structural pattern (tree-sitter) typical file set: <400ms.

---
## 10. Assistant / API Surface
- **CLI**:
  - `codesearch index [--full|--incremental] [path]`
  - `codesearch query "How is OAuth token refreshed?" --limit 30 --mode auto --json`
  - `codesearch struct "pattern: ..."`
  - `codesearch explain <file>:<start>-<end>` (LLM summarization; uses retrieved snippet + local model)
- **Local HTTP/gRPC** (optional daemon):
  - `POST /v1/query { q, mode?, limit? }`
  - `POST /v1/struct { pattern }`
  - `POST /v1/refactor { description }`
  - `POST /v1/embed { text[] }`
  - JSON response includes: `score, path, start_line, end_line, symbol, strategy, snippet, highlight_ranges`.
- **RAG Usage**: Provide top-N enriched with symbol signature + minimal AST path; assistant composes higher-level answers.

---
## 11. NL → Structural Translation
- **Pipeline**:
  1. Classify intent: (security, refactor, pattern, semantic, question).
  2. If structural candidate: feed NL into prompt template with examples → produce Semgrep / tsquery skeleton.
  3. Validate pattern (dry-run). If zero matches AND confidence low → fallback to semantic + show suggestion.
  4. Cache successful translation keyed by normalized NL query hash.
- **Model**: Small instruct (e.g., `phi-2`, `mistral-7b-instruct` quantized) or fallback to prompt-tuned embedding model for classification.

---
## 12. Privacy & Security
- All computation local; no outbound calls unless explicit user opt-in.
- Provide environment guard `OFFLINE=1` to hard-fail external requests.
- Strip / redact secrets during indexing preview (heuristics: entropy + regex for tokens) → optional.

---
## 13. Performance Considerations
- **Incremental AST**: tree-sitter incremental parse reduces re-index cost (only changed ranges).
- **Parallelism**: File scanning & AST parse pool (bounded by cores); embedding generation micro-batches.
- **IO Minimization**: Content hash before reading full file if size + mtime unchanged (use stable file hashing strategy).
- **Vector Search**: Use DuckDB VSS; fallback to brute-force for dim <512 & small corpus (<50k vectors).
- **Warm Cache**: Lazy load embeddings table; memory-map via DuckDB; optionally prefetch top symbol vectors.

---
## 14. Incremental Indexing Workflow
1. Detect changed / new / deleted files.
2. For changed: recompute content hash; if identical → skip.
3. Re-parse AST (changed regions) → update symbol spans & affected chunks only.
4. Rebuild chunks for impacted symbols (avoid regenerating unaffected neighbors).
5. Reuse embedding via hash; if miss → batch embed.
6. Vacuum orphan rows periodically (deleted files → cascade semantics or manual GC task).

---
## 15. Evaluation & Metrics
- **Relevance**: MRR@10, nDCG@10 using curated query → gold snippet set.
- **Latency**: P50, P95 for (semantic query, structural query, hybrid pipeline).
- **Index Freshness**: Time from file save → index update (target <1.5s).
- **Embedding Cache Hit Rate**: >90% after initial full build typical commit workflow.
- **Recall vs. Chunk Strategy**: A/B symbol-only vs. symbol+window.

---
## 16. Phased Roadmap
| Phase | Focus | Key Deliverables |
|-------|-------|------------------|
| 0 | Skeleton | CLI scaffold, DuckDB schema, file scanning + hashing |
| 1 | Structural Core | tree-sitter parse, symbol table, TSQuery search, Telescope picker basic |
| 2 | Semantic Layer | Embedding generation + vector search + hybrid ranking prototype |
| 3 | NL Translation | Intent classifier + NL→structural pattern LLM integration |
| 4 | Optimization | Incremental updates, caching, latency tuning, evaluation harness |
| 5 | Advanced | Semgrep integration, clone detection seeds, call graph prototype |

---
## 17. Risks & Mitigations
- **Embedding Model Latency**: Use quantized small model; batch aggressively; preload session.
- **Large Repos Memory**: Offload large `content` columns to compressed external store (future) or partial load.
- **Pattern Translation Errors**: Provide transparent view: show generated pattern diff + allow manual edit.
- **Semantic Drift**: Periodic evaluation harness; log low-score accepted results for retraining heuristics.
- **DuckDB VSS Maturity**: Abstract vector store interface → fallback to brute-force or alternative (faiss / sqlite-vss) without changing higher layers.

---
## 18. Open Questions & Future Enhancements
- Add **symbol relationship graph** (imports, inheritance) for improved rerank.
- Introduce **lightweight call graph** (function-level edges) for intent expansion.
- Integrate **embeddings distillation** (model ensemble → smaller vector) for footprint reduction.
- Explore **PQ / IVF** indexing if vectors exceed ~500k scale.
- Add **contextual snippet summarization** cache for AI assistants (store short natural language description per symbol).
- Implement **active learning loop**: user click feedback → rerank weight refinement.
- Support **multi-language hybrid queries** (e.g., "Python FastAPI auth middleware definition").

---
## 19. Implementation Notes (Practical)
- **Language Coverage**: Prioritize: Python, TypeScript/JavaScript, Go, Rust, Java; add others incrementally.
- **Symbol Extraction**: Use tree-sitter queries per language to enumerate top-level definitions.
- **Config File**: `.localcodesearch.yml` to define include/exclude globs, embedding model, max file size.
- **Concurrency**: Use a task queue (e.g., asyncio in Python) with backpressure for embedding generation.
- **Testing**: Golden test corpus (small synthetic repo) verifying structural + semantic retrieval correctness.
- **Dev Ergonomics**: `codesearch doctor` command to validate environment (DuckDB ext available, model present/quantized).

---
## 20. Minimal MVP Slice (Concrete Checklist)
1. DuckDB schema (files, symbols, chunks, embeddings).
2. tree-sitter parsing + symbol & chunk creation (no semantics yet).
3. CLI: `index`, `query` (keyword + symbol name fuzzy), `struct` (simple TSQuery).
4. Telescope picker integration for `:CodeSearch` (keyword & symbol name only).
5. Basic evaluation harness scaffold.

Then layer embeddings + hybrid logic.

---
## 21. Example CLI Interactions (Future)
```
$ codesearch index ./repo
Indexed 1,240 files (fresh: 98 new, 34 updated) in 11.2s
Embeddings: 6,540 (cache hits: 87%)

$ codesearch query "how are jwt tokens validated" --limit 5
[semantic+struct hybrid]
1 auth/jwt.py:44-71  score=0.92  def validate_access_token(...)
2 middleware/security.py:10-33  score=0.87
...

$ codesearch struct "(function_definition name: (identifier) @fn (#match? @fn "(?i)refresh"))"
```

---
## 22. Summary
This design specifies a layered, offline-first architecture combining fast structural parsing (tree-sitter / Semgrep) with semantic vector retrieval in DuckDB via its VSS extension. A strategy selection layer routes queries among pure structural, pure semantic, or hybrid pipelines, while a translation subsystem lowers natural language to formal structural queries using a small local LLM. The system emphasizes incremental indexing, extensibility, and low-latency Telescope integration, forming a foundation for advanced assistant and analysis features (clone detection, call graph, RAG augmentation) in subsequent phases.

---
(End of Plan)
