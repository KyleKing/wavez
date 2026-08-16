# ADR-001: Hybrid Vector + AST Code Search Architecture

**Status:** Proposed
**Date:** 2025-09-22
**Deciders:** Development Team
**Technical Story:** Implement offline-first, hybrid code search system combining structural and semantic search capabilities

## Context

Modern code search requires both precise structural pattern matching and semantic understanding of code intent. Existing solutions often excel in one dimension but lack comprehensive coverage. We need a solution that:

- Operates completely offline for privacy-sensitive environments
- Combines structural (AST-based) and semantic (embedding-based) search
- Integrates seamlessly with developer workflows (Neovim Telescope)
- Provides programmatic APIs for AI assistant integration
- Handles large codebases (100k-2M LOC) efficiently

## Decision Drivers

1. **Privacy First**: Complete offline operation with no external dependencies
2. **Hybrid Intelligence**: Leverage both structural patterns and semantic understanding
3. **Developer Experience**: Sub-second search latency with intuitive interfaces
4. **AI Integration**: Clean APIs for RAG pipelines and coding assistants
5. **Scalability**: Handle mono-repos and multi-repo workspaces efficiently
6. **Extensibility**: Foundation for advanced features (call graphs, clone detection)

## Considered Options

### Option A: Proposed Hybrid Architecture (DuckDB + tree-sitter + Local LLM)

**Architecture:**
- DuckDB with VSS extension for unified relational + vector storage
- tree-sitter for fast, incremental AST parsing
- Local embedding models (e5-small-v2, jina-embeddings-v2-base-code)
- Hybrid query orchestrator with strategy selection
- NL → structural pattern translation via local LLM

**Novel Features:**
- Unified storage layer combining relational metadata and vector similarity
- Multi-granularity chunking (symbol-level + sliding window)
- Intent-driven query routing (structural vs semantic vs hybrid)
- Real-time NL → structural pattern translation
- Incremental indexing with content-hash deduplication

### Option B: Extended Existing Tools

**Architecture Options:**
- Sourcegraph + custom semantic layer
- ripgrep + separate vector database
- ast-grep + Chroma/Qdrant integration
- GitHub Copilot + Semgrep combination

### Option C: Pure Semantic Approach

**Architecture:**
- Focus entirely on embedding-based search
- Large context windows with transformer models
- Code-specific embedding models (CodeBERT, GraphCodeBERT)

### Option D: Pure Structural Approach

**Architecture:**
- Advanced pattern matching with Semgrep/CodeQL
- Graph-based code analysis
- Traditional text-based search with regex

## Detailed Comparison

| Capability | Proposed Hybrid | Sourcegraph | GitHub Copilot | ast-grep + Vector DB | ripgrep + Embeddings | Pure Semantic | Pure Structural |
|------------|----------------|-------------|----------------|-------------------|------------------|---------------|----------------|
| **Core Functionality** |
| Offline Operation | ✅ Complete | ❌ Cloud/Enterprise | ❌ Cloud-based | ✅ Complete | ✅ Complete | ✅ Possible | ✅ Complete |
| Structural Search | ✅ tree-sitter + Semgrep | ✅ Limited patterns | ❌ Limited | ✅ Advanced DSL | ❌ Regex only | ❌ None | ✅ Advanced |
| Semantic Search | ✅ Local embeddings | ✅ Basic | ✅ Advanced | ⚠️ Separate system | ⚠️ Separate system | ✅ Advanced | ❌ None |
| Hybrid Ranking | ✅ Weighted fusion | ❌ Separate tools | ❌ Black box | ❌ Manual integration | ❌ Manual integration | ❌ Single mode | ❌ Single mode |
| **Novel Differentiators** |
| Unified Storage | ✅ DuckDB relational+vector | ❌ Separate systems | ❌ Proprietary | ❌ Separate DBs | ❌ Separate systems | ⚠️ Vector only | ⚠️ Metadata only |
| NL→Pattern Translation | ✅ Local LLM pipeline | ❌ Manual patterns | ⚠️ Limited | ❌ Manual DSL | ❌ Manual regex | ❌ N/A | ❌ Manual patterns |
| Multi-granularity Chunks | ✅ Symbol + window levels | ❌ File-based | ❌ Proprietary | ⚠️ AST nodes only | ❌ Line-based | ⚠️ Fixed windows | ❌ Match-based |
| Intent Classification | ✅ Query routing | ❌ Manual selection | ❌ Black box | ❌ Manual mode | ❌ Manual mode | ❌ Single intent | ❌ Single intent |
| Incremental AST Updates | ✅ tree-sitter incremental | ❌ Full reparse | ❌ Unknown | ✅ Incremental | ❌ N/A | ❌ N/A | ⚠️ Depends on tool |
| **Integration & APIs** |
| Neovim Integration | ✅ Native Telescope | ⚠️ Custom plugin | ⚠️ Extension-based | ⚠️ Custom integration | ⚠️ Custom integration | ⚠️ Custom integration | ⚠️ Custom integration |
| AI Assistant APIs | ✅ Structured JSON+context | ⚠️ Basic REST | ❌ Proprietary | ❌ No standard API | ❌ No standard API | ⚠️ Vector similarity only | ⚠️ Match results only |
| Programmatic Access | ✅ CLI + HTTP/gRPC | ✅ GraphQL API | ❌ Proprietary | ⚠️ CLI only | ⚠️ CLI only | ⚠️ Custom implementation | ⚠️ CLI only |
| **Performance & Scale** |
| Large Repo Support | ✅ Optimized for 2M LOC | ✅ Enterprise scale | ✅ Cloud scale | ⚠️ Memory limited | ✅ Excellent | ⚠️ Memory intensive | ✅ Excellent |
| Query Latency | ✅ <300ms target | ⚠️ Network dependent | ⚠️ Network dependent | ✅ <100ms structural | ⚠️ Depends on setup | ⚠️ Model dependent | ✅ <50ms |
| Index Freshness | ✅ <1.5s incremental | ⚠️ Minutes to hours | ❌ Unknown cadence | ✅ Real-time | ❌ Manual refresh | ⚠️ Manual refresh | ✅ Real-time |
| Memory Efficiency | ✅ DuckDB optimization | ⚠️ High memory | ❌ Unknown | ⚠️ AST in memory | ⚠️ Vector storage | ❌ High memory | ✅ Minimal |
| **Advanced Features** |
| Pattern Caching | ✅ Structural match cache | ❌ Limited | ❌ Unknown | ❌ No caching | ❌ No caching | ❌ No caching | ⚠️ Tool-dependent |
| Content Deduplication | ✅ Hash-based embedding cache | ❌ No dedup | ❌ Unknown | ❌ No dedup | ❌ No dedup | ❌ Usually no dedup | ❌ No dedup |
| Multi-repo Workspace | ✅ Designed for multi-repo | ✅ Enterprise feature | ⚠️ Limited | ⚠️ Manual setup | ⚠️ Manual setup | ⚠️ Manual setup | ⚠️ Manual setup |
| Extensibility | ✅ Plugin architecture planned | ⚠️ Limited extensibility | ❌ Closed system | ⚠️ Rule-based only | ⚠️ Limited | ⚠️ Model-dependent | ✅ Pattern extensible |

**Legend:**
- ✅ Excellent/Complete support
- ⚠️ Partial/Limited support
- ❌ Not supported/Not available

## Novel Value Propositions

### 1. **Unified Query Intelligence**
Unlike existing tools that require manual mode selection, our hybrid orchestrator automatically routes queries based on intent classification:
- "Where is OAuth implemented?" → Semantic search
- "Find all SQL injection patterns" → Structural search
- "Functions similar to authenticate_user but for admin" → Hybrid approach

### 2. **NL → Structural Pattern Translation**
First-of-its-kind local LLM pipeline that translates natural language directly to tree-sitter queries or Semgrep patterns:
```
"Find functions that don't validate input parameters"
→ (function_definition parameters: (parameters) @params body: (block) @body
   (#not-match? @body "validate|check|sanitize"))
```

### 3. **Content-Hash Based Deduplication**
Intelligent caching system that prevents redundant computation:
- Same code content across files/repos shares embeddings
- Incremental updates only recompute changed content
- 90%+ cache hit rates in typical development workflows

### 4. **Multi-Granularity Semantic Search**
Search operates at both symbol-level (precise) and window-level (contextual) simultaneously:
- Symbol-level: Find exact function definitions
- Window-level: Discover related implementation patterns
- Hybrid ranking combines both for optimal recall/precision

### 5. **DuckDB Analytical Foundation**
Leverages DuckDB's analytical capabilities for advanced queries:
- Complex JOINs between code structure and semantic similarity
- Temporal analysis of code evolution
- Statistical insights into codebase patterns
- Foundation for future call graph and clone detection features

## Risks and Mitigations

### Technical Risks

1. **DuckDB VSS Extension Maturity**
   - **Risk:** VSS extension may have performance or stability issues
   - **Mitigation:** Abstract vector store interface; fallback to brute-force or alternative (faiss/sqlite-vss)

2. **Local LLM Performance**
   - **Risk:** Pattern translation accuracy and latency
   - **Mitigation:** Quantized models, aggressive caching, transparent fallback to semantic search

3. **Memory Usage at Scale**
   - **Risk:** Large repositories may exceed memory limits
   - **Mitigation:** Streaming processing, partial index loading, configurable chunk sizes

### Product Risks

1. **User Adoption Complexity**
   - **Risk:** Hybrid system may be confusing vs. simple grep
   - **Mitigation:** Intelligent defaults, progressive disclosure, clear feedback on query strategy

2. **Index Maintenance Overhead**
   - **Risk:** Users may find incremental indexing disruptive
   - **Mitigation:** Background processing, clear progress indicators, emergency fallback modes

## Implementation Strategy

### Phase 0: Foundation (Weeks 1-2)
- DuckDB schema and basic file scanning
- tree-sitter integration for core languages
- CLI scaffold with basic commands

### Phase 1: Structural Core (Weeks 3-4)
- Symbol extraction and indexing
- TSQuery pattern matching
- Basic Telescope integration

### Phase 2: Semantic Layer (Weeks 5-7)
- Local embedding model integration
- Vector search implementation
- Hybrid ranking prototype

### Phase 3: Intelligence Layer (Weeks 8-10)
- Intent classification system
- NL → pattern translation
- Query orchestrator

### Phase 4: Optimization (Weeks 11-12)
- Performance tuning
- Incremental indexing
- Production readiness

## Decision

**Selected Option A: Hybrid Architecture**

This architecture provides the most comprehensive solution that addresses all decision drivers while offering genuinely novel capabilities not available in existing tools. The combination of unified storage, intelligent query routing, and local LLM integration creates a new category of code search tool.

The key differentiators justify the implementation complexity:
1. True offline operation with enterprise-grade capabilities
2. Automatic query intelligence vs. manual tool selection
3. Unified storage eliminating integration complexity
4. Novel NL → structural pattern translation
5. Foundation for advanced code analysis features

## Consequences

### Positive
- Pioneering hybrid search approach sets new industry standard
- Complete privacy compliance for sensitive codebases
- Extensible architecture enables future advanced features
- Strong foundation for AI assistant integration
- Novel capabilities create competitive moat

### Negative
- Higher initial implementation complexity vs. existing tools
- Dependency on relatively new DuckDB VSS extension
- Requires local LLM infrastructure setup
- More moving parts than single-purpose tools

### Neutral
- Learning curve for users familiar with traditional tools
- Storage requirements higher than pure text search
- Performance characteristics different from cloud-based solutions

## Related Decisions

- **ADR-002**: Local LLM Model Selection and Quantization Strategy
- **ADR-003**: DuckDB Schema Evolution and Migration Strategy
- **ADR-004**: Incremental Indexing Implementation Details
- **ADR-005**: Telescope Integration Architecture

## References

- [Vector and AST Code Search Implementation Plan](./Vector%20and%20AST%20Code%20Search%20Implementation%20Plan.md)
- [DuckDB VSS Extension Documentation](https://github.com/duckdb/duckdb_vss)
- [tree-sitter Incremental Parsing](https://tree-sitter.github.io/tree-sitter/using-parsers#editing)
- [Semgrep Pattern Syntax](https://semgrep.dev/docs/writing-rules/pattern-syntax/)