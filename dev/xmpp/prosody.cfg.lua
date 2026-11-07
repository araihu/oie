-- Local development only. The published ports bind to host loopback.
daemonize = false
pidfile = "/var/run/prosody/prosody.pid"
data_path = "/var/lib/prosody"
admins = { "alice@localhost" }
authentication = "internal_hashed"
storage = "sql"
sql = { driver = "SQLite3"; database = "prosody.sqlite" }
log = { { levels = { min = "info" }; to = "console" } }

modules_enabled = {
  "disco"; "roster"; "saslauth"; "tls"; "smacks"; "carbons";
  "mam"; "pep"; "ping"; "websocket";
}
modules_disabled = { "s2s" }
allow_registration = false
c2s_require_encryption = true
c2s_ports = { 5222 }
http_ports = {}
https_ports = { 5281 }
ssl = {
  key = "/var/lib/prosody/certs/localhost.key";
  certificate = "/var/lib/prosody/certs/localhost.crt";
}
archive_expires_after = "never"
default_archive_policy = true
max_archive_query_results = 50

VirtualHost "localhost"

Component "conference.localhost" "muc"
  name = "Oie development rooms"
  restrict_room_creation = "local"
  modules_enabled = { "muc_mam" }
  muc_room_default_persistent = true
  muc_room_default_public = true
  muc_room_default_members_only = false

Component "upload.localhost" "http_file_share"
  http_host = "localhost"
  http_external_url = "https://localhost:5281/"
  http_file_share_size_limit = 20 * 1024 * 1024
  http_file_share_expires_after = "7d"
