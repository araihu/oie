//go:build (linux || darwin) && cgo && !server

package desktop

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestStageNativeAttachmentUsesSelectedFile(t *testing.T) {
	const content = "contents from the document portal"
	const name = "conversação.txt"
	const engine = "019a5df0-0000-7000-8000-000000000001"
	const conversation = "019a5df0-0000-7000-8000-000000000002"
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query()
		if !nativeRequest(r) || r.URL.Path != "/ui/scheduling/stage" || r.Method != http.MethodPost ||
			query.Get("engineId") != engine || query.Get("conversationId") != conversation ||
			r.Header.Get("X-CSRF-Token") != "test-csrf" {
			t.Fatal("staging request differs from the composer's contract")
		}
		reader, err := r.MultipartReader()
		if err != nil {
			t.Fatal(err)
		}
		part, err := reader.NextPart()
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(part)
		if err != nil || string(data) != content || part.FileName() != name || part.FormName() != "file" {
			t.Fatalf("wrong attachment part: %q, %q, %q, %v", data, part.FileName(), part.FormName(), err)
		}
		_ = json.NewEncoder(w).Encode(engineapi.StagedAttachment{Id: query.Get("attachmentId"), FileName: name, Size: int64(len(data))})
	})

	staged, err := stageNativeAttachment(t.Context(), handler, path, engine, conversation, "test-csrf")
	if err != nil || staged.FileName != name || staged.Size != int64(len(content)) || !validUUID(staged.Id) {
		t.Fatalf("wrong staged attachment: %+v, %v", staged, err)
	}
}
