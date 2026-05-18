package brain

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func testBrain(t *testing.T) *Brain {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	b, err := Init(dbPath, nil, DefaultOptions())
	if err != nil {
		t.Fatalf("init brain: %v", err)
	}
	t.Cleanup(func() { b.Close() })
	return b
}

func TestInit(t *testing.T) {
	b := testBrain(t)
	stats, err := b.Stats(context.Background())
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if stats.Pages != 0 {
		t.Errorf("expected 0 pages, got %d", stats.Pages)
	}
	// Default source should be seeded
	if stats.Sources != 1 {
		t.Errorf("expected 1 source, got %d", stats.Sources)
	}
}

func TestIngestPage(t *testing.T) {
	b := testBrain(t)
	ctx := context.Background()

	page := Page{
		SourceID: "default",
		Slug:     "test/hello-world",
		Type:     "note",
		Title:    "Hello World",
		Content:  "This is a test note about Raspberry Pi and Go programming.",
	}
	id, err := b.IngestPage(ctx, page)
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if id <= 0 {
		t.Errorf("expected positive id, got %d", id)
	}

	// Verify retrieval
	got, err := b.GetPageByID(ctx, id)
	if err != nil {
		t.Fatalf("get page: %v", err)
	}
	if got.Title != "Hello World" {
		t.Errorf("expected title 'Hello World', got %q", got.Title)
	}

	// Verify stats
	stats, _ := b.Stats(ctx)
	if stats.Pages != 1 {
		t.Errorf("expected 1 page, got %d", stats.Pages)
	}
}

func TestIngestPageDedup(t *testing.T) {
	b := testBrain(t)
	ctx := context.Background()

	page := Page{
		SourceID: "default",
		Slug:     "test/same",
		Title:    "Original",
		Content:  "Same content",
	}
	id1, _ := b.IngestPage(ctx, page)

	page.Title = "Updated"
	id2, _ := b.IngestPage(ctx, page)

	// Same slug should update, not create new
	if id1 != id2 {
		t.Errorf("expected same id for upsert, got %d and %d", id1, id2)
	}

	stats, _ := b.Stats(ctx)
	if stats.Pages != 1 {
		t.Errorf("expected 1 page after dedup, got %d", stats.Pages)
	}

	// Title should be updated
	got, _ := b.GetPageByID(ctx, id1)
	if got.Title != "Updated" {
		t.Errorf("expected updated title, got %q", got.Title)
	}
}

func TestContentHash(t *testing.T) {
	h1 := contentHash("hello world")
	h2 := contentHash("hello world")
	h3 := contentHash("different")

	if h1 != h2 {
		t.Error("same content should produce same hash")
	}
	if h1 == h3 {
		t.Error("different content should produce different hash")
	}
}

func TestFTS5Search(t *testing.T) {
	b := testBrain(t)
	ctx := context.Background()

	pages := []Page{
		{Slug: "note1", Title: "Raspberry Pi Setup", Content: "How to set up a Raspberry Pi with Go and SQLite for embedded projects"},
		{Slug: "note2", Title: "Cooking Recipe", Content: "A great recipe for chocolate cake with raspberry filling"},
		{Slug: "note3", Title: "Go Concurrency", Content: "Patterns for concurrent programming in Go using goroutines and channels"},
	}
	for _, p := range pages {
		b.IngestPage(ctx, p)
	}

	results, err := b.Search(ctx, "Raspberry Pi Go", SearchOpts{Limit: 5, KeywordOnly: true})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected search results, got none")
	}
	// First result should be about Raspberry Pi + Go
	if results[0].Title != "Raspberry Pi Setup" {
		t.Errorf("expected 'Raspberry Pi Setup' as top result, got %q", results[0].Title)
	}
}

func TestRRFFusion(t *testing.T) {
	b := testBrain(t)

	fts := []rankedResult{
		{PageID: 1, Score: -1.5, Title: "Doc A"},
		{PageID: 2, Score: -2.1, Title: "Doc B"},
		{PageID: 3, Score: -3.0, Title: "Doc C"},
	}
	vec := []rankedResult{
		{PageID: 3, Score: 0.95, Title: "Doc C"},
		{PageID: 1, Score: 0.85, Title: "Doc A"},
		{PageID: 4, Score: 0.70, Title: "Doc D"},
	}

	merged := b.rrfFuse(fts, vec)
	if len(merged) != 4 {
		t.Fatalf("expected 4 merged results, got %d", len(merged))
	}

	// Doc A (rank 0 in FTS, rank 1 in vec) and Doc C (rank 2 FTS, rank 0 vec)
	// should score highest. Doc 4 only appears in vec.
	if merged[0].PageID != 1 && merged[0].PageID != 3 {
		t.Errorf("expected Doc A or Doc C as top, got PageID %d", merged[0].PageID)
	}

	// Doc 4 should be last (only appears in one list)
	lastMerged := merged[len(merged)-1]
	if lastMerged.PageID != 4 && lastMerged.PageID != 2 {
		t.Errorf("expected Doc B or Doc D at bottom, got PageID %d", lastMerged.PageID)
	}
}

func TestCosineSimilarity(t *testing.T) {
	a := []float32{1, 0, 0}
	b := []float32{1, 0, 0}
	c := []float32{0, 1, 0}
	d := []float32{-1, 0, 0}

	if s := cosineSimilarity(a, b); s < 0.99 {
		t.Errorf("identical vectors should have similarity ~1.0, got %f", s)
	}
	if s := cosineSimilarity(a, c); s > 0.01 {
		t.Errorf("orthogonal vectors should have similarity ~0.0, got %f", s)
	}
	if s := cosineSimilarity(a, d); s > -0.99 {
		t.Errorf("opposite vectors should have similarity ~-1.0, got %f", s)
	}
}

func TestVectorBlobRoundTrip(t *testing.T) {
	original := []float32{0.1, 0.2, 0.3, -0.4, 0.5}
	blob := vectorToBlob(original)
	recovered := blobToVector(blob)

	if len(recovered) != len(original) {
		t.Fatalf("length mismatch: %d vs %d", len(recovered), len(original))
	}
	for i := range original {
		if recovered[i] != original[i] {
			t.Errorf("index %d: expected %f, got %f", i, original[i], recovered[i])
		}
	}
}

func TestIngestDir(t *testing.T) {
	b := testBrain(t)
	ctx := context.Background()

	// Create test directory structure
	dir := t.TempDir()
	os.MkdirAll(filepath.Join(dir, "notes"), 0o755)
	os.WriteFile(filepath.Join(dir, "notes", "test.md"), []byte("# Test Note\nThis is about Go programming."), 0o644)
	os.WriteFile(filepath.Join(dir, "other.txt"), []byte("Some text file content"), 0o644)

	imported, err := b.IngestDir(ctx, "default", dir)
	if err != nil {
		t.Fatalf("ingest dir: %v", err)
	}
	if imported != 2 {
		t.Errorf("expected 2 imported files, got %d", imported)
	}

	// Second import should skip (content-hash dedup)
	imported2, _ := b.IngestDir(ctx, "default", dir)
	if imported2 != 0 {
		t.Errorf("expected 0 on re-import, got %d", imported2)
	}
}

func TestSlugify(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Hello World", "hello-world"},
		{"Test/Path/File", "test-path-file"},
		{"  spaces  ", "spaces"},
		{"Multiple---Dashes", "multiple-dashes"},
	}
	for _, tt := range tests {
		got := slugify(tt.in)
		if got != tt.want {
			t.Errorf("slugify(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestDetectType(t *testing.T) {
	tests := []struct{ path, want string }{
		{"people/alice.md", "person"},
		{"companies/acme.md", "company"},
		{"conversations/chat1.md", "conversation"},
		{"ideas/big-idea.md", "concept"},
		{"2026-05-18.md", "daily"},
		{"random-note.md", "note"},
	}
	for _, tt := range tests {
		got := detectType(tt.path)
		if got != tt.want {
			t.Errorf("detectType(%q) = %q, want %q", tt.path, got, tt.want)
		}
	}
}

func TestParseFrontmatter(t *testing.T) {
	content := "---\ntitle: Test\nauthor: Bob\ndate: 2026-01-01\n---\n\nContent here"
	meta := parseFrontmatter(content)
	if meta["title"] != "Test" {
		t.Errorf("expected title=Test, got %q", meta["title"])
	}
	if meta["author"] != "Bob" {
		t.Errorf("expected author=Bob, got %q", meta["author"])
	}

	// No frontmatter
	meta2 := parseFrontmatter("Just content")
	if len(meta2) != 0 {
		t.Errorf("expected empty metadata, got %v", meta2)
	}
}

// mockEmbedder is a test embedding provider that produces deterministic vectors.
type mockEmbedder struct {
	dims int
}

func (m *mockEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	vec := make([]float32, m.dims)
	for i := range vec {
		vec[i] = float32(i%100) / 100.0
	}
	return vec, nil
}

func (m *mockEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	vecs := make([][]float32, len(texts))
	for i := range vecs {
		v, _ := m.Embed(ctx, texts[i])
		vecs[i] = v
	}
	return vecs, nil
}

func (m *mockEmbedder) ModelName() string { return "mock" }

func TestBrainWithEmbeddings(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")
	embedder := &mockEmbedder{dims: 64}
	opts := DefaultOptions()
	opts.EmbeddingDims = 64

	b, err := Init(dbPath, embedder, opts)
	if err != nil {
		t.Fatalf("init: %v", err)
	}
	defer b.Close()

	ctx := context.Background()
	b.IngestPage(ctx, Page{Slug: "test", Title: "Test", Content: "Hello world"})

	// backfillEmbedding runs in a goroutine — give it a moment
	// In production this is fire-and-forget; in tests we wait briefly
	time.Sleep(100 * time.Millisecond)

	stats, _ := b.Stats(ctx)
	if stats.Embeddings != 1 {
		t.Errorf("expected 1 embedding, got %d", stats.Embeddings)
	}
}
