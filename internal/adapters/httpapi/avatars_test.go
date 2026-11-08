package httpapi_test

import (
	"bytes"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/araihu/oie/internal/adapters/avatarfiles"
	"github.com/araihu/oie/internal/adapters/httpapi/engineapi"
)

func TestAvatarMetadataBinaryAndConditionalCache(t *testing.T) {
	dir := t.TempDir()
	e, _, server := newMessagingEngine(t, dir, &protocolStub{})
	e.SetAvatarFiles(avatarfiles.New(dir + "/media/avatars"))
	c := openTestConversation(t, e, "alice@localhost")
	var imageData bytes.Buffer
	if err := png.Encode(&imageData, image.NewGray(image.Rect(0, 0, 8, 8))); err != nil {
		t.Fatal(err)
	}
	if err := e.StoreAvatar(t.Context(), c.AccountID, "bob@localhost", "private-source-token", imageData.Bytes()); err != nil {
		t.Fatal(err)
	}
	res, err := http.Get(server.URL + "/api/v1/accounts/" + string(c.AccountID) + "/avatars")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	var page engineapi.AvatarPage
	if res.StatusCode != 200 || json.Unmarshal(body, &page) != nil || len(page.Items) != 1 || strings.Contains(string(body), "private-source-token") {
		t.Fatal("metadata contract", res.StatusCode, string(body))
	}
	hash := page.Items[0].ImageHash
	imageURL := server.URL + "/api/v1/avatars/" + hash
	res, err = http.Get(imageURL)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("Content-Type") != "image/png" || res.Header.Get("X-Content-Type-Options") != "nosniff" {
		t.Fatal("image contract", res.StatusCode, res.Header)
	}
	if _, err = png.Decode(bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	req, _ := http.NewRequest("GET", imageURL, nil)
	req.Header.Set("If-None-Match", "W/"+res.Header.Get("ETag"))
	res, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 304 {
		t.Fatal("conditional GET", res.StatusCode)
	}
	for _, test := range []struct {
		suffix string
		status int
	}{{strings.Repeat("0", 64), 404}, {"invalid", 400}} {
		res, err = http.Get(server.URL + "/api/v1/avatars/" + test.suffix)
		if err != nil {
			t.Fatal(err)
		}
		res.Body.Close()
		if res.StatusCode != test.status {
			t.Fatal("bad image response", test, res.StatusCode)
		}
	}
}
