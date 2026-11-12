// Command scheduling-preview serves the scheduling design with the pinned Goshtoso component.
package main

import (
	"bytes"
	"context"
	"flag"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/a-h/templ"
	"github.com/araihu/goshtoso/assets"
	"github.com/araihu/goshtoso/components/dropdown"
	"github.com/araihu/goshtoso/components/head"
	"github.com/araihu/goshtoso/components/link"
	"github.com/araihu/goshtoso/components/splitbutton"
)

func main() {
	listen := flag.String("listen", "127.0.0.1:58887", "preview address")
	flag.Parse()

	ctx := context.Background()
	var dependencies, navigation, composer bytes.Buffer
	if err := head.Dependencies(head.WithLocalRuntime()).Render(ctx, &dependencies); err != nil {
		log.Fatal(err)
	}
	for _, item := range []struct{ id, label string }{
		{"chats", "Conversas"},
		{"scheduled", "Agendadas"},
	} {
		if err := link.Link("#"+item.id, link.WithRootClass("main-nav-link"),
			link.WithAttrs(templ.Attributes{"data-view": item.id, "data-copy": item.id}),
		).Render(templ.WithChildren(ctx, templ.Raw(templ.EscapeString(item.label))), &navigation); err != nil {
			log.Fatal(err)
		}
	}
	if err := splitbutton.SplitButton(splitbutton.Config{
		ID: "composer-actions", MenuLabel: "Opções de envio", MenuAlign: dropdown.AlignEnd,
		Primary: splitbutton.Action{
			ID: "send-action", Label: "Enviar", OnClick: "document.getElementById('composer').requestSubmit()",
		},
		Sections: []dropdown.Section{{Items: []dropdown.Item{{
			ID: "schedule-action", Label: "Agendar mensagem", OnClick: "window.openSchedulingDialog()",
		}}}},
	}).Render(ctx, &composer); err != nil {
		log.Fatal(err)
	}

	mux := http.NewServeMux()
	mux.Handle("GET /assets/", assets.Handler())
	mux.HandleFunc("GET /message-scheduling.html", func(w http.ResponseWriter, _ *http.Request) {
		page, err := os.ReadFile("docs/prototypes/message-scheduling.html")
		if err != nil {
			http.Error(w, "Could not read the preview.", http.StatusInternalServerError)
			log.Print(err)
			return
		}
		page = bytes.Replace(page, []byte("<!-- goshtoso-dependencies -->"), dependencies.Bytes(), 1)
		page = bytes.Replace(page, []byte("<!-- main-navigation -->"), navigation.Bytes(), 1)
		page = bytes.Replace(page, []byte("<!-- composer-actions -->"), composer.Bytes(), 1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(page)
	})
	server := &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	log.Fatal(server.ListenAndServe())
}
