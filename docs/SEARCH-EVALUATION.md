# Search evaluation: vector store and embedding model

A decision record. It says what membraid's semantic search is built on, what
was measured to choose it, and why the alternatives lost, so the question does
not need reopening without new evidence.

Measured in September 2026.

## Decision summary

- **Vector store: pure Go over the existing SQLite index** (modernc.org/sqlite,
  no CGO).
  - **What is stored:** each memory's full float32 vector plus a sign-bit binary
    code.
  - **How it is searched:**
    - Long-lived MCP servers keep the binary codes in memory.
    - Short CLI calls scan them through a covering index.
    - Either way, the top 200 candidates are rescored by exact cosine.
  - No extension, no index training, no driver change.
- **Embeddings are off by default**, and `membraid install` offers them.
  - When Ollama is present, the provider is EmbeddingGemma 300M QAT q4_0.
  - Otherwise it is all-MiniLM-L6-v2, built into the binary through hugot's
    pure-Go backend.
- **With embeddings on, search is vector-only.** Keyword results are not fused
  in.
- **Keyword search is fixed**, and it is what runs whenever embeddings are off:
  - NLTK English stopwords are dropped.
  - Identifier-like tokens are kept whole.
  - The remaining terms are ORed and ranked by bm25.
- **Vectors live in each machine's local index, never in the synced wire log.**
  They are derived data: changing model re-embeds locally, and the locked log
  format is untouched.

## Constraints that shaped it

- **One binary with no CGO**, cross-compiling to Windows and macOS.
- **Many concurrent processes on one vault:** several harnesses' MCP servers,
  CLI calls and the sync timer.
- **Privacy:** local models only. Cloud embedding APIs are deferred by choice,
  because memory would leave the user's machines.

## Vector store evaluation

### Method

- **Real embeddings, not synthetic ones:**
  - Qdrant's DBpedia set (OpenAI text-embedding-3-small, 1024 dimensions, 100K
    rows).
  - KShivendu's DBpedia set (OpenAI embeddings, 1536 dimensions, 1M rows).
  - The last 200 rows of each were held out as queries.
  - A first synthetic dataset turned out to be near-random, so it was discarded
    for recall. It made binary quantisation look far worse (0.72 recall at 200
    candidates) than it is on real data (0.9975).
- **Every query exercised membraid's needs:**
  - A scope filter (`scope IN ('p3','shared')`).
  - Retired rows that must be excluded.
- **Recall@10** is measured against exact search over the matching rows.
- **Machine:** Intel Core Ultra 5 125U (14 threads, 30 GB). Timings are from a
  sequential pass on a near-idle machine (load average about 1.0 to 1.4),
  not an isolated benchmark host.
- **Fresh process** means a new process that opens the database and runs one
  query, the CLI case. **Long-lived** means one process serving many queries,
  the MCP-server case.

### Pure Go, 100K rows x 1024 dimensions

Build: 4.4 s, 0.52 GB database.

| Strategy | Candidates | Recall@10 | Long-lived p50 | Fresh process p50 | Notes |
|---|---|---|---|---|---|
| Exact scan | - | 1.000 | 132.8 ms | 139.0 ms | |
| Bits in memory | 50 | 0.9475 | 0.67 ms | | |
| Bits in memory | 100 | 0.9840 | 0.93 ms | | |
| **Bits in memory** | **200** | **0.9975** | **1.42 ms** | 82.6 ms | 74 ms load, 61 MB RSS |
| Bits in memory | 500 | 1.000 | 2.98 ms | | |
| **Bits via covering index** | **200** | **0.9975** | 16.5 ms | **26.5 ms** | about 20 MB RSS |

### Pure Go, 1M rows x 1536 dimensions

Build: 104 s, 8.54 GB database.

| Strategy | Candidates | Recall@10 | Long-lived p50 | Fresh process p50 | Notes |
|---|---|---|---|---|---|
| Exact scan | - | 1.000 | 1713 ms | 1635 ms | |
| Bits in memory | 50 | 0.9575 | 6.11 ms | | |
| **Bits in memory** | **200** | **0.997** | **7.31 ms** | 926 ms | 883 ms load, 693 MB RSS |
| Bits in memory | 500 | 0.999 | 9.33 ms | | |
| **Bits via covering index** | **200** | **0.997** | 181 ms | **187 ms** | about 22 MB RSS |

### vec1 (SQLite's vector extension) via ncruces `ext/vec1`, Go/WASM, 100K x 1024

- **Version:** vec1 0.7 in ncruces/go-sqlite3 v0.35.4. `vec1_info()` reports
  "Scalar, single-threaded".
- **Index:** IVF with quantizer `none`, 316 buckets.
- **Cost:** training took 170 s and rebuild 50 s.
- **Memory:** the rebuild needs `sqlite3.WithMaxMemory` raised from ncruces's
  256 MB per-connection default. The default fails with "out of memory", and
  4 GiB is the hard ceiling.

| Buckets searched (nprobe) | Recall@10 | p50 |
|---|---|---|
| 2% | 0.7955 | 2.77 ms |
| 5% (default) | 0.8875 | 6.01 ms |
| 20% | 0.9685 | 30.8 ms |
| 50% | 0.994 | 73.7 ms |
| All buckets | 1.000 | 129.0 ms |

- **Fresh process** (default nprobe 5%, recall 0.8875): 19.0 ms.
- **Flat exact index:** 110.4 ms.
- **Compressed PQ index** (32-byte codes), same Go build, measured under load:
  recall caps at 0.8965 even with all buckets and 200 reranked, at 249 ms. The
  whole run took about 15 minutes.
- **Native C reference** (AVX2, not shippable without CGO), OPQ with 32-byte
  codes:
  - 0.9885 recall at 6.6 ms searching 50%.
  - 0.993 at 12.0 ms searching all buckets.
  - Training took 84 s on 8 threads. Native PQ peaked at 0.901.

### sqlite-vec via ncruces bindings, 100K x 1024

- **Build:** `sqlite-vec-go-bindings` v0.1.7-alpha.2 on ncruces/go-sqlite3
  v0.23.3.
- **Search:** exact (brute force), recall 1.000. Query p50 117.6 ms, p95
  121.2 ms.
- **Open and prepare:** 616 ms. Peak RSS 1.0 GB.
- **Fresh process:** 745 ms without a WASM compile cache, 138 ms with one.
- **Build:** 14.8 s, 397 MB database.

### Why not vec1

- **Slower for the same recall** in the build membraid can ship. Reaching 0.994
  takes 73.7 ms; pure Go reaches 0.9975 in 1.42 ms (long-lived) or 26.5 ms
  (fresh process). The WASM build is scalar and single-threaded, and SIMD for
  WASM is still on vec1's roadmap.
- **An index lifecycle to manage:**
  - The index must be trained on sample data before it works.
  - Rows inserted afterwards go into the existing buckets without retraining.
  - Something has to schedule rebuilds as the store grows.
- **Maturity:**
  - 0.7 lacks the August 2026 trunk fixes. These include streaming queries that
    "omit some rows and duplicate others", and problems combining `IN(...)` with
    other metadata constraints, which is exactly membraid's scope filter.
  - The docs say "Testing is insufficient."
  - One native run failed `PRAGMA integrity_check`.
- **A driver switch:** it would move membraid from modernc to ncruces. ncruces
  uses OFD locks and modernc uses POSIX locks, so the two must not share a vault
  file across processes.
- **Binary size:** grows to 18 to 20 MB.
- **Where it would win:** native CGO builds, or a future vec1 1.0 with WASM
  SIMD. Revisit then.

### Why not sqlite-vec

- **Frozen Go bindings:** they are paused and only work with ncruces v0.19.0 to
  v0.23.3, which pins SQLite 3.47.2. Current ncruces moved to wasm2go and
  dropped the hook the bindings rely on.
- **No approximate index:** ANN exists only in upstream alphas.
- **Slow start:** each new process spends about 650 ms compiling the WASM unless
  a compile cache is configured.

### Why not Turso or libSQL

- **libSQL:** the embedded Go driver requires CGO.
- **Turso Database (the Rust rewrite):**
  - It is pre-1.0.
  - Its full-text search (tantivy-based) and multi-process mode are
    experimental.
  - Its approximate vector index is still on the roadmap.
- **Turso Cloud** remains a candidate backend for a possible hosted membraid,
  which would be a different product.

### Why not graph RAG

- **Cost:** it needs an LLM call on every write to extract entities and
  relations, and extraction errors compound.
- **Fit:** membraid's memories are short standalone facts that agents write
  deliberately, not documents to mine. Keys plus supersession already give
  every subject a timeline.

## Embedding model evaluation

### Dataset

- **150 invented memories** across 6 fictional projects plus `shared`, written
  like what coding agents record.
- **Deliberate near-duplicates**, for example different production hosts and
  test commands per project.
- **100 queries in four categories:**
  - 50 **paraphrase** queries. A checker verified they share no stemmed content
    words with their target memories.
  - 20 **natural** questions.
  - 15 **identifier** lookups (env vars, paths, error codes, versions).
  - 15 **distractor** queries, where the obvious lexical match is the wrong
    project.
- **Scope:** English only. Search ran across all projects with no scope filter,
  which is harder than real use.
- **Metric:** R@5 is the fraction of queries with a relevant memory in the top
  5; R@1 is the same for the top 1.
- **Prompts:** each model used its model card's query and document prompts.

### Results

| Provider | R@5 | R@1 | Query p50 | RAM | Download |
|---|---|---|---|---|---|
| **EmbeddingGemma 300M QAT q4_0 (Ollama)** | **0.96** | **0.81** | 47.8 ms | 297 MB | 239 MB |
| EmbeddingGemma 300M QAT q8_0 (Ollama) | 0.95 | 0.80 | 57.8 ms | 397 MB | 338 MB |
| EmbeddingGemma 300M full (Ollama) | 0.95 | 0.80 | 57.7 ms | 680 MB | 622 MB |
| EmbeddingGemma truncated to 256 dims | 0.87 to 0.91 | 0.73 to 0.74 | | | |
| Qwen3-Embedding 0.6B (Ollama) | 0.92 | 0.69 | 126 ms | 2371 MB | 639 MB |
| all-MiniLM (Ollama) | 0.91 | 0.61 | 7.9 ms | 48 MB | 46 MB |
| **all-MiniLM-L6-v2, pure Go (hugot)** | **0.91** | 0.61 | 53 ms | 286 MB | 91 MB |
| bge-m3 (Ollama) | 0.86 | 0.64 | 122 ms | 1219 MB | 1158 MB |
| bge-small-en-v1.5, pure Go | 0.84 | 0.61 | 147 ms | 420 MB | 134 MB |
| nomic-embed-text (Ollama) | 0.81 | 0.58 | 41 ms | 376 MB | 274 MB |
| Keyword search, fixed | 0.47 | 0.42 | 0.08 ms | - | - |
| Keyword search as it was | 0.15 | 0.15 | 0.04 ms | - | - |

**Notes:**
- **Pure-Go all-MiniLM matches Ollama's all-MiniLM exactly.** The vectors agree
  to about 1e-4, and it adds about 16 MB to the binary.
- **bge-small is under-reported:** hugot only mean-pools, while bge-small is
  meant to use its [CLS] token.

### By category, R@5

| | Paraphrase | Natural | Identifier | Distractor |
|---|---|---|---|---|
| EmbeddingGemma q4_0 | 0.92 | 1.00 | 1.00 | 1.00 |
| Keyword search, fixed | 0.00 | 0.95 | 1.00 | 0.87 |
| Keyword search as it was | 0.00 | 0.00 | 1.00 | 0.00 |

**The old keyword bug:** it quoted every token and FTS5 ANDed them, so any
question containing a word like "what" or "does" matched nothing.

The fixed keyword search is good when the query shares words with the memory,
and finds nothing when the wording differs. Embeddings handle both.

### Fusion findings

- **RRF fusion of vectors with fixed keyword search made results worse:** R@5
  fell from 0.91-0.96 to 0.65-0.72 across models. Paraphrase queries still share
  common words with *wrong* memories, and RRF rewards anything that appears in
  both lists.
- **Weighting the vector list double** barely helped (0.68-0.72).
- **Fusing only when the query contains an identifier** was slightly worse than
  vector-only (for example 0.93 against 0.96 for EmbeddingGemma q4_0). Ordinary
  hyphenated words triggered the gate on 5 paraphrase and 2 distractor queries.
- **Boosting only exact identifier matches** tied vector-only. It was never more
  than one query better (Qwen3 0.93 against 0.92, bge-m3 0.87 against 0.86).
- **Hence vector-only when embeddings are on.**

## Ollama resource note

- **Idle server:** `ollama serve` measured 60 MB RSS with no model loaded.
- **Models load on demand** and unload after `keep_alive` (5 minutes by
  default).
- **So membraid can rely on a running Ollama** without keeping a model in
  memory. EmbeddingGemma q4_0 takes about 300 MB only while embedding.

## Limits of this evidence

- **The embedding dataset is small, invented and English-only.** Real agent
  queries are probably less paraphrase-heavy, so fixed keyword search will do
  somewhat better in practice than 0.47.
- **Everything ran on one laptop.** The 4-core mini PC and Windows are untested,
  deferred by choice.
- **Timings come from a near-idle machine, not an isolated host.**

## Reproducing

- **The harnesses lived outside the repo** during the evaluation.
- **The embedding test set** (150 memories, 100 queries) is being added to the
  repo under `internal/index/testdata/search/`, so keyword and vector search
  changes can be measured against it.

## Sources

- SQLite vec1: https://sqlite.org/vec1
- sqlite-vec: https://github.com/asg017/sqlite-vec
- sqlite-vec ANN tracking issue: https://github.com/asg017/sqlite-vec/issues/25
- sqlite-vec Go bindings: https://github.com/asg017/sqlite-vec-go-bindings
- ncruces/go-sqlite3: https://github.com/ncruces/go-sqlite3
- ncruces VFS locking and WAL support: https://pkg.go.dev/github.com/ncruces/go-sqlite3/vfs
- go-libsql: https://github.com/tursodatabase/go-libsql
- Turso Database: https://github.com/tursodatabase/turso
- tursogo: https://pkg.go.dev/turso.tech/database/tursogo
- coder/hnsw: https://pkg.go.dev/github.com/coder/hnsw
- EmbeddingGemma: https://huggingface.co/google/embeddinggemma-300m and https://developers.googleblog.com/en/introducing-embeddinggemma/
- EmbeddingGemma Ollama tags: https://ollama.com/library/embeddinggemma/tags
- Qwen3-Embedding: https://huggingface.co/Qwen/Qwen3-Embedding-0.6B
- nomic-embed-text: https://huggingface.co/nomic-ai/nomic-embed-text-v1.5
- bge-m3: https://huggingface.co/BAAI/bge-m3
- all-MiniLM-L6-v2: https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2
- hugot: https://github.com/knights-analytics/hugot
- Datasets: https://huggingface.co/datasets/Qdrant/dbpedia-entities-openai3-text-embedding-3-small-1024-100K and https://huggingface.co/datasets/KShivendu/dbpedia-entities-openai-1M
