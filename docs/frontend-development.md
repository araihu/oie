# Frontend reload loop

Run the engine separately, then start the frontend supervisor from the repository:

```sh
make dev-web
```

The default engine URL is `http://127.0.0.1:18084`. The BFF listens on
`0.0.0.0:8080`, including forwarded preview connections. Override either address:

```sh
make dev-web DEV_ENGINE_URL=http://127.0.0.1:18084 DEV_WEB_LISTEN=127.0.0.1:8082
```

Air v1.67.4 is pinned in the tools module. It generates templ output and builds
`bin/oie-dev-web` after Go, templ, JavaScript, CSS and embedded asset edits.
Generated `_templ.go` files, tests, tools, binaries, data and dependency folders
do not cause rebuild loops. A failed build keeps the previous BFF running and
prints its error. Restart the supervisor after changing its configuration.

Only the BFF restarts. The separately launched engine keeps its database,
accounts and protocol sessions. Engine code changes require an explicit engine
restart; rebuilding the frontend does not apply them to that process.

The development BFF uses `--dev-reload`. Its initial HTML contains a process
marker and a small browser script that checks `/ui/dev/build` every two seconds
while the page is visible. The browser reloads after a successful BFF restart
and retries through downtime. Normal launches omit the script and return 404
for the marker and script endpoints. This facility is for local development.

Air's reverse proxy is disabled. Its app-start timeout also bounds upstream
response bodies in the pinned release, which would interrupt long media and
live-event streams. The browser talks directly to the BFF's normal HTTP server.
See the [Air configuration and proxy documentation](https://github.com/air-verse/air/tree/v1.67.4)
for the upstream tool's behavior.
