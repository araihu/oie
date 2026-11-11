#!/usr/bin/env python3
"""Exercise the installed native WebView against disposable data and a fake API.

Uses Wails' build-tagged MCP driver. No provider account or XMPP traffic is used.
"""
import contextlib
import json
import os
from pathlib import Path
import secrets
import shutil
import socket
import subprocess
import sys
import tempfile
import threading
import time
import urllib.error
import urllib.parse
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

ROOT = Path(__file__).resolve().parents[2]
ARTIFACTS = ROOT / "tests/desktop/artifacts"
ARTIFACTS.mkdir(parents=True, exist_ok=True)
BINARY = Path(os.environ.get("OIE_DESKTOP_BINARY", ROOT / "bin/oie-desktop-smoke")).resolve()


def uid(n):
    return "019a5df0-0000-7000-8000-%012d" % n


ENGINE, ACCOUNT, CHAT, MESSAGE, ITEM = (uid(n) for n in range(1, 6))
NOW = "2026-11-10T12:00:00Z"


def require(condition, message):
    if not condition:
        raise AssertionError(message)


class Fixture(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.0"
    posts = 0
    range_reads = 0
    content_reads = 0
    stream_reads = 0
    state_lock = threading.Lock()
    messages = [{"id": MESSAGE, "revisionId": uid(6), "accountId": ACCOUNT,
                 "conversationId": CHAT, "sender": {"id": uid(7), "displayName": "Bob fixture"},
                 "originalText": "Native WebView fixture", "createdAt": NOW,
                 "isOwn": False, "sendState": "received"}]

    def log_message(self, *_):
        pass

    def reply(self, status, data, mime="application/json", headers=None):
        body = json.dumps(data).encode() if mime == "application/json" else data
        self.send_response(status)
        self.send_header("Content-Type", mime)
        self.send_header("Content-Length", str(len(body)))
        for key, value in (headers or {}).items():
            self.send_header(key, value)
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        path = urllib.parse.urlsplit(self.path).path
        account = {"id": ACCOUNT, "revisionId": uid(8), "address": "alice@example.test",
                   "protocol": "xmpp", "label": "Alice fixture", "createdAt": NOW,
                   "connectionState": "not_connected"}
        conversation = {"id": CHAT, "revisionId": uid(9), "accountId": ACCOUNT,
                        "kind": "direct", "address": "bob@example.test", "title": "Bob fixture",
                        "lastMessage": self.messages[-1]["originalText"], "updatedAt": NOW,
                        "unreadCount": 0, "archived": False, "favorite": False}
        if path == "/api/v1/engine":
            return self.reply(200, {"id": ENGINE, "role": "hosted", "apiVersion": "0.2.0", "eventCursor": ENGINE + ":0"})
        if path == "/api/v1/accounts":
            return self.reply(200, {"items": [account]})
        if path == "/api/v1/accounts/" + ACCOUNT:
            return self.reply(200, account)
        if path.endswith("/connection"):
            return self.reply(200, {"accountId": ACCOUNT, "state": "not_connected", "updatedAt": NOW})
        if path.endswith("/history"):
            return self.reply(200, {"accountId": ACCOUNT, "state": "complete", "retainedMessages": len(self.messages), "conversations": 1, "updatedAt": NOW})
        if path == "/api/v1/conversations":
            return self.reply(200, {"items": [conversation]})
        if path == "/api/v1/conversations/" + CHAT:
            return self.reply(200, conversation)
        if path == "/api/v1/conversations/" + CHAT + "/read-state":
            return self.reply(200, {"conversationId": CHAT, "accountId": ACCOUNT, "unreadCount": 0})
        if path == "/api/v1/previews/" + uid(30):
            return self.reply(200, {"id": uid(30), "revisionId": uid(31), "accountId": ACCOUNT,
                                   "conversationId": CHAT, "messageId": MESSAGE, "kind": "image", "state": "ready",
                                   "downloadState": "queued", "url": "https://files.example.test/photo.png", "updatedAt": NOW})
        if path == "/api/v1/shared-content/" + ITEM + "/thumbnail":
            return self.reply(200, {"id": uid(32), "revisionId": uid(33), "accountId": ACCOUNT,
                                   "conversationId": CHAT, "messageId": MESSAGE, "sharedContentId": ITEM,
                                   "kind": "video", "state": "not_requested", "url": "https://files.example.test/video.mp4", "updatedAt": NOW})
        if path == "/api/v1/conversations/" + CHAT + "/messages":
            return self.reply(200, {"items": list(reversed(self.messages))})
        if path == "/api/v1/messages/" + MESSAGE:
            return self.reply(200, self.messages[0])
        if path.endswith("/previews") or path.endswith("/stars") or path.endswith("/reactions") or path.endswith("/contacts"):
            return self.reply(200, {"items": []})
        if path == "/api/v1/events":
            with self.state_lock:
                Fixture.stream_reads += 1
            self.send_response(200)
            self.send_header("Content-Type", "text/event-stream")
            self.send_header("Cache-Control", "no-cache")
            self.end_headers()
            try:
                for n in range(120):
                    self.wfile.write(("event: desktop.smoke\ndata: %s\n\n" % json.dumps({"sequence": n})).encode())
                    self.wfile.flush()
                    time.sleep(.2)
            except (BrokenPipeError, ConnectionResetError):
                pass
            return
        if path == "/api/v1/shared-content/" + ITEM + "/content":
            Fixture.content_reads += 1
            data = b"0123456789"
            headers = {"Accept-Ranges": "bytes", "Content-Disposition": 'inline; filename="native-fixture.txt"'}
            status = 200
            if self.headers.get("Range") == "bytes=2-5":
                Fixture.range_reads += 1
                data, status = b"2345", 206
                headers["Content-Range"] = "bytes 2-5/10"
            return self.reply(status, data, "text/plain", headers)
        return self.reply(404, {"type": "urn:oie:problem:not_found", "title": "Not found", "code": "not_found", "status": 404})

    def do_POST(self):
        body = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))) or b"{}")
        if urllib.parse.urlsplit(self.path).path == "/api/v1/conversations/" + CHAT + "/read-state":
            return self.reply(200, {"conversationId": CHAT, "accountId": ACCOUNT, "unreadCount": 0})
        if urllib.parse.urlsplit(self.path).path == "/api/v1/conversations/" + CHAT + "/messages":
            Fixture.posts += 1
            message = {"id": uid(100 + self.posts), "revisionId": uid(200 + self.posts), "accountId": ACCOUNT,
                       "conversationId": CHAT, "sender": {"id": ACCOUNT, "displayName": "Alice fixture"},
                       "originalText": body["originalText"], "createdAt": NOW, "isOwn": True, "sendState": "queued"}
            self.messages.append(message)
            return self.reply(202, message)
        return self.reply(404, {"status": 404, "code": "not_found"})


def free_port():
    with socket.socket() as sock:
        sock.bind(("127.0.0.1", 0))
        return sock.getsockname()[1]


class Native:
    def __init__(self, profile, name, engine=None):
        self.port = free_port()
        self.token = secrets.token_urlsafe(24)
        self.seq = 0
        self.log = (ARTIFACTS / (name + ".log")).open("w")
        self.args = [str(BINARY), "--data-dir", str(profile), "--lang", "en"]
        if engine:
            self.args += ["--engine-url", engine]
        env = dict(os.environ, WAILS_MCP_PORT=str(self.port), WAILS_MCP_HOST="127.0.0.1",
                   WAILS_MCP_TOKEN=self.token, WAILS_MCP_HIDE_CURSOR="true")
        self.process = subprocess.Popen(self.args, env=env, stdout=self.log, stderr=subprocess.STDOUT)
        try:
            self.wait(lambda: self.rpc("initialize", {"protocolVersion": "2025-06-18", "capabilities": {},
                                                      "clientInfo": {"name": "oie-native-smoke", "version": "1"}}), "MCP startup")
            self.wait(lambda: self.js("return !!document.querySelector('#oie-app') && typeof htmx !== 'undefined';", 2000), "BFF and Wails runtime")
        except BaseException:
            self.close()
            raise

    def rpc(self, method, params):
        self.seq += 1
        request = urllib.request.Request("http://127.0.0.1:%s/mcp" % self.port,
                  data=json.dumps({"jsonrpc": "2.0", "id": self.seq, "method": method, "params": params}).encode(),
                  headers={"Content-Type": "application/json", "Authorization": "Bearer " + self.token})
        with urllib.request.urlopen(request, timeout=35) as response:
            result = json.load(response)
        if "error" in result:
            raise RuntimeError(result["error"])
        return result["result"]

    def tool(self, name, args=None):
        result = self.rpc("tools/call", {"name": name, "arguments": args or {}})
        if result.get("isError"):
            raise RuntimeError(result)
        value = result["content"][0]["text"]
        try:
            return json.loads(value)
        except ValueError:
            return value

    def js(self, source, timeout=15000):
        return self.tool("js_eval", {"window": "oie-main", "js": source, "timeout_ms": timeout})

    def wait(self, callback, name):
        deadline = time.monotonic() + 45
        last = None
        while time.monotonic() < deadline:
            require(self.process.poll() is None, "native app exited during " + name)
            try:
                value = callback()
                if value:
                    return value
            except (OSError, RuntimeError) as error:
                last = error
            time.sleep(.15)
        raise AssertionError("timed out: %s (%s)" % (name, last))

    def navigate(self, query):
        self.js("window.__nativeDocumentMarker='preserved'; htmx.ajax('GET', %s, {target:'#oie-app',swap:'outerHTML'});return true;" % json.dumps("/?" + query))
        self.wait(lambda: self.js("return !!document.querySelector('#oie-app') && !document.querySelector('.htmx-request');"), "fragment settled")
        require(self.js("return window.__nativeDocumentMarker==='preserved';"), "fragment navigation reloaded the document")

    def quit(self):
        try:
            self.js("const r=await import('/wails/runtime.js'); r.Application.Quit(); return true;", 1000)
        except (OSError, RuntimeError):
            pass
        self.process.wait(timeout=15)
        require(self.process.returncode == 0, "explicit Quit failed: " + str(self.process.returncode))

    def close(self):
        try:
            if self.process.poll() is None:
                try:
                    self.process.terminate()
                except ProcessLookupError:
                    pass
                try:
                    self.process.wait(timeout=15)
                except subprocess.TimeoutExpired:
                    self.log.write("Native smoke cleanup force-killed its owned process %s\n" % self.process.pid)
                    self.log.flush()
                    self.process.kill()
                    self.process.wait(timeout=15)
        finally:
            self.log.close()


@contextlib.contextmanager
def native(*args, **kwargs):
    app = Native(*args, **kwargs)
    try:
        yield app
    finally:
        app.close()


def linux_dialog_diagnostics(stage, app, target):
    """Capture only this test's private Xvfb, including native GTK windows."""
    if os.environ.get("OIE_DESKTOP_PRIVATE_DISPLAY") != "1":
        return
    prefix = ARTIFACTS / ("gtk-chooser-" + stage)
    commands = {
        "tree": ["xwininfo", "-root", "-tree"],
        "focus": ["xdotool", "getwindowfocus", "getwindowname"],
    }
    for kind, command in commands.items():
        if shutil.which(command[0]):
            result = subprocess.run(command, capture_output=True, text=True, timeout=10)
            prefix.with_suffix("." + kind + ".txt").write_text(result.stdout + result.stderr)
    if shutil.which("scrot"):
        subprocess.run(["scrot", "-o", str(prefix.with_suffix(".png"))], capture_output=True, timeout=10)
    state = {"target": str(target), "targetExists": target.exists(), "contentReads": Fixture.content_reads}
    try:
        state["app"] = app.tool("app_info")
    except (OSError, RuntimeError) as error:
        state["appError"] = str(error)
    prefix.with_suffix(".json").write_text(json.dumps(state, indent=2) + "\n")


def linux_save_dialog(app, target, cancel=False):
    """Drive GTK in this test's private Xvfb. Never sends input on a host display."""
    if sys.platform != "linux":
        return
    require(os.environ.get("OIE_DESKTOP_PRIVATE_DISPLAY") == "1", "GTK automation requires tools/ci/desktop.sh private Xvfb")
    source = "/ui/shared-content/" + ITEM + "/content?engineId=" + ENGINE + "&download=1"
    previous_reads = Fixture.content_reads
    if cancel:
        app.js("window.__nativeSaveResult='pending';window.OieDesktop.saveFile(%s,'native-fixture.txt').then(value=>window.__nativeSaveResult=value).catch(error=>window.__nativeSaveResult=String(error));return true;" % json.dumps(source))
    else:
        app.js("const a=document.createElement('a');a.href=%s;a.download='native-fixture.txt';document.body.append(a);a.click();a.remove();return true;" % json.dumps(source))

    def dialog_window():
        result = subprocess.run(["xdotool", "search", "--onlyvisible", "--name", "^Save file$"], capture_output=True, text=True)
        return result.stdout.splitlines()[0] if result.returncode == 0 and result.stdout.strip() else None

    dialog = app.wait(dialog_window, "native GTK save chooser")
    subprocess.run(["xdotool", "windowfocus", "--sync", dialog], check=True, timeout=5)
    linux_dialog_diagnostics("opened-cancel" if cancel else "opened", app, target)
    if cancel:
        subprocess.run(["xdotool", "key", "--clearmodifiers", "Escape"], check=True, timeout=5)
        app.wait(lambda: not dialog_window(), "native GTK chooser cancelled")
        app.wait(lambda: app.js("return window.__nativeSaveResult === false;"), "native GTK cancellation result")
        require(not target.exists(), "cancel created a file")
        require(Fixture.content_reads == previous_reads, "cancel unnecessarily read the original")
        return
    subprocess.run(["xdotool", "key", "--clearmodifiers", "ctrl+l"], check=True, timeout=5)
    # GTK selects the filename stem here and retains its extension. Replace all.
    subprocess.run(["xdotool", "key", "--clearmodifiers", "ctrl+a"], check=True, timeout=5)
    linux_dialog_diagnostics("location-focus", app, target)
    subprocess.run(["xdotool", "type", "--clearmodifiers", "--delay", "1", str(target)], check=True, timeout=5)
    linux_dialog_diagnostics("path-entered", app, target)
    subprocess.run(["xdotool", "key", "--clearmodifiers", "Return"], check=True, timeout=5)
    time.sleep(.5)
    linux_dialog_diagnostics("return", app, target)
    if dialog_window():
        subprocess.run(["xdotool", "key", "--clearmodifiers", "alt+s"], check=True, timeout=5)
    linux_dialog_diagnostics("save", app, target)
    try:
        app.wait(target.exists, "native saved original")
    except BaseException:
        linux_dialog_diagnostics("failed", app, target)
        raise
    require(target.read_bytes() == b"0123456789", "native chooser saved different bytes")
    app.wait(lambda: not dialog_window(), "native GTK chooser closed")


def main():
    findings = []
    report = ARTIFACTS / "report.json"
    report.unlink(missing_ok=True)
    server = ThreadingHTTPServer(("127.0.0.1", 0), Fixture)
    threading.Thread(target=server.serve_forever, daemon=True).start()
    engine = "http://127.0.0.1:%s" % server.server_port
    try:
        with tempfile.TemporaryDirectory(prefix="oie-desktop-smoke-") as temporary:
            root = Path(temporary)
            profile = root / "remote-profile"
            with native(profile, "remote-first", engine) as app:
                origin = app.js("return {href:location.href,origin:location.origin,url:new URL('/ui/events',location.origin).href};")
                require(origin["origin"] == "wails://localhost", "native origin URL semantics: " + str(origin))
                app.navigate("chat=" + CHAT + "&lang=pt-BR&theme=light")
                app.wait(lambda: app.js("return !!document.querySelector('#message-composer');"), "composer")
                launch_csrf = app.js("return document.querySelector('#send-message-form input[name=csrf]').value;")
                # Native CookieJar must keep BFF CSRF and explicit device preferences.
                require(app.js("return (await fetch('/api/v1/engine')).status;") == 404, "native assets exposed the structured API")
                statuses = app.js("""const f=document.querySelector('#send-message-form');const form=new URLSearchParams(new FormData(f));form.set('originalText','native accepted');const bad=new URLSearchParams(form);bad.set('csrf','forged');const forged=await fetch(f.action,{method:'POST',body:bad,headers:{Accept:'application/json'}});const good=await fetch(f.action,{method:'POST',body:form,headers:{Accept:'application/json'}});return {forged:forged.status,good:good.status};""")
                require(statuses == {"forged": 403, "good": 200}, "BFF cookie/CSRF mutation failed: " + str(statuses))
                require(Fixture.posts == 1, "forged request reached the engine")
                findings.append("native BFF CSRF rejects forged token and accepts the real form token")
                proxy_checks = app.js("""const headers={'X-Oie-Engine-ID':%s,'X-Oie-Account-ID':%s};const readURL=%s;const read=await fetch(readURL,{headers});const csrf=document.querySelector('#send-message-form input[name=csrf]').value;const post=await fetch(readURL,{method:'POST',headers:{...headers,'X-CSRF-Token':csrf,'Idempotency-Key':%s,'Content-Type':'application/json'},body:JSON.stringify({messageIds:[%s]})});const preview=await fetch(%s,{headers});const thumbnail=await fetch(%s,{headers});return {read:{status:read.status,body:await read.json()},post:{status:post.status,body:await post.json()},preview:{status:preview.status,body:await preview.json()},thumbnail:{status:thumbnail.status,body:await thumbnail.json()}};""" % tuple(map(json.dumps, [ENGINE, ACCOUNT, "/api/v1/conversations/" + CHAT + "/read-state", uid(800), MESSAGE, "/api/v1/previews/" + uid(30), "/api/v1/shared-content/" + ITEM + "/thumbnail"])))
                require(all(value["status"] == 200 for value in proxy_checks.values()), "native scoped BFF JSON proxies: " + str(proxy_checks))
                require(proxy_checks["read"]["body"]["conversationId"] == CHAT and proxy_checks["post"]["body"]["accountId"] == ACCOUNT, "read-state source binding")
                require(proxy_checks["preview"]["body"]["id"] == uid(30) and proxy_checks["thumbnail"]["body"]["sharedContentId"] == ITEM, "preview polling source binding")
                findings.append("native scoped read-state GET/CSRF POST and preview/thumbnail polling pass with the engine identity header")

                # Exercise actual HTMX navigation, not just fetch/DOM replacement.
                app.js("window.__nativeDocumentMarker='preserved';document.querySelector('a[href*=" + json.dumps("section=media") + "]').click();return true;")
                app.wait(lambda: app.js("return !!document.querySelector('#shared-content-search');"), "HTMX navigation")
                require(app.js("return window.__nativeDocumentMarker==='preserved';"), "HTMX navigation reloaded document")
                app.navigate("chat=" + CHAT)
                app.wait(lambda: app.js("return !!document.querySelector('#message-composer');"), "composer after HTMX")
                source = "/ui/shared-content/" + ITEM + "/content?engineId=" + ENGINE
                ranges = app.js("const r=await fetch(%s,{headers:{Range:'bytes=2-5'}});return {status:r.status,text:await r.text(),range:r.headers.get('content-range')};" % json.dumps(source))
                require(ranges == {"status": 206, "text": "2345", "range": "bytes 2-5/10"}, "native media Range: " + str(ranges))
                download = app.js("const r=await fetch(%s);return {status:r.status,text:await r.text(),disposition:r.headers.get('content-disposition')};" % json.dumps(source + "&download=1"))
                require(download["status"] == 200 and download["text"] == "0123456789" and "attachment" in download["disposition"], "download bytes/disposition")
                events = app.js("return await new Promise(resolve=>{const e=new EventSource('/ui/events'),timer=setTimeout(()=>{e.close();resolve(false)},6000);e.addEventListener('desktop.smoke',()=>{clearTimeout(timer);e.close();resolve(true)})});", 9000)
                require(events and Fixture.range_reads > 0 and Fixture.stream_reads > 0, "native EventSource/Range fixture not reached")
                findings.append("HTMX keeps the document; native EventSource, byte ranges, original download bytes and disposition pass")
                if sys.platform == "linux":
                    linux_save_dialog(app, root / "saved-native-fixture.txt")
                    linux_save_dialog(app, root / "cancelled-native-fixture.txt", cancel=True)
                    findings.append("actual GTK Save chooser writes exact original bytes; Cancel creates no file")
                draft = "Native persistent draft 👋"
                app.js("const c=document.querySelector('#message-composer');c.value=%s;c.dispatchEvent(new Event('input',{bubbles:true}));localStorage.setItem('oie.native.smoke','persistent');return true;" % json.dumps(draft))
                app.wait(lambda: app.js("return Object.keys(localStorage).some(k=>k.startsWith('oie.draft.v1')&&localStorage.getItem(k).includes('Native persistent draft'));"), "draft committed")
                # Window Close must hide, then a second invocation must reopen it.
                app.tool("window_control", {"window": "oie-main", "action": "close"})
                app.wait(lambda: not app.tool("app_info")["windows"][0]["visible"], "close hides window")
                require(app.process.poll() is None, "close stopped embedded runtime")
                second = subprocess.run(app.args, stdout=subprocess.PIPE, stderr=subprocess.STDOUT, timeout=20)
                (ARTIFACTS / "second-instance.log").write_bytes(second.stdout)
                require(second.returncode == 0, "second instance failed: " + second.stdout.decode(errors="replace"))
                app.wait(lambda: app.tool("app_info")["windows"][0]["visible"], "second invocation reopens window")
                require(app.js("return document.querySelector('#message-composer').value;") == draft, "hide/reopen lost draft")
                findings.append("close hides the window and second invocation reopens the original instance without losing its draft")
                app.quit()
            with native(profile, "remote-restart", engine) as app:
                require(app.js("return document.documentElement.lang;") == "pt-BR", "language cookie did not survive restart")
                require(app.js("return document.documentElement.dataset.mode;") == "light", "theme cookie did not survive restart")
                require(app.js("return localStorage.getItem('oie.native.smoke');") == "persistent", "WebView localStorage did not survive restart")
                app.navigate("chat=" + CHAT)
                app.wait(lambda: app.js("return document.querySelector('#message-composer')?.value;") == draft, "draft restored after restart")
                require(app.js("return document.querySelector('#send-message-form input[name=csrf]').value;") != launch_csrf, "CSRF did not rotate across Quit")
                submitted = app.js("const f=document.querySelector('#send-message-form'),values=new URLSearchParams(new FormData(f));const stale=new URLSearchParams(values);stale.set('csrf',%s);const old=await fetch(f.action,{method:'POST',body:stale,headers:{Accept:'application/json'}});const fresh=await fetch(f.action,{method:'POST',body:values,headers:{Accept:'application/json'}});return {stale:old.status,fresh:fresh.status,body:await fresh.json()};" % json.dumps(launch_csrf))
                require(submitted["stale"] == 403 and submitted["fresh"] == 200 and submitted["body"]["ok"], "restart mutation/CSRF: " + str(submitted))
                require(Fixture.posts == 2 and Fixture.messages[-1]["originalText"] == draft, "restored draft was not sent exactly once")
                app.js("localStorage.removeItem('oie.native.smoke');return true;")
                app.quit()
                findings.append("exact device language/theme, WebView storage and draft survive Quit; a fresh CSRF token sends the restored draft and the old token is rejected")
            # Verify the actual embedded engine, fresh and never connected to XMPP.
            local_profile = root / "embedded-profile"
            with native(local_profile, "embedded-first") as app:
                engine_id = app.js("return document.querySelector('#oie-app').dataset.engineId;")
                require(engine_id and engine_id != ENGINE, "embedded engine was not started")
                app.navigate("open=accounts&accountPage=add")
                app.wait(lambda: app.js("return !!document.querySelector('#register-account-form');"), "register form")
                result = app.js("const f=document.querySelector('#register-account-form'),values=new URLSearchParams(new FormData(f));values.set('address','offline@example.test');values.set('label','Native offline fixture');const r=await fetch(f.action,{method:'POST',body:values,headers:{Accept:'application/json'}});return {status:r.status,body:await r.json()};")
                require(result["status"] == 200 and result["body"]["ok"], "embedded account registration: " + str(result))
                app.tool("window_control", {"window": "oie-main", "action": "close"})
                app.wait(lambda: not app.tool("app_info")["windows"][0]["visible"], "embedded window hides")
                require(app.js("const r=await fetch('/');return (await r.text()).includes('Native offline fixture');"), "hidden embedded engine stopped serving")
                app.tool("window_control", {"window": "oie-main", "action": "show"})
                app.quit()
            with native(local_profile, "embedded-restart") as app:
                require(app.js("return document.querySelector('#oie-app').dataset.engineId;") == engine_id, "embedded engine identity changed after restart")
                require(app.js("return document.body.textContent.includes('Native offline fixture');"), "embedded SQLite account did not survive Quit")
                app.quit()
                findings.append("embedded engine keeps serving while hidden; explicit Quit closes cleanly and SQLite identity/account survive restart")
        report.write_text(json.dumps({"platform": sys.platform, "status": "passed", "checks": findings}, indent=2) + "\n")
        for finding in findings:
            print("PASS:", finding, flush=True)
    except BaseException as error:
        report.write_text(json.dumps({"platform": sys.platform, "status": "failed", "checks": findings, "error": str(error)}, indent=2) + "\n")
        raise
    finally:
        server.shutdown()
        server.server_close()


if __name__ == "__main__":
    main()
