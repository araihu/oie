# Installer checks

Run the shell installer regression suite with Python's standard library:

```sh
python3 -m unittest discover -s tests/packaging -p 'test_*.py'
```

The tests execute `scripts/install.sh` with fresh temporary user and install
paths. A `curl` shim accepts only the expected GitHub release URLs and copies
fixture archives and checksums. Platform and library checks are simulated through
PATH shims. No download reaches the network, no provider account is used, and
no package manager or privileged command may execute.

Coverage includes platform selection, default and custom destinations, optional
FFmpeg, missing runtime guidance, checksums and archive validation, literal
Desktop Entry paths, updates, failed moves, rollback after SIGTERM, preservation
of user data, and cleanup. Desktop Entry assertions decode both the general
[string escapes](https://specifications.freedesktop.org/desktop-entry/latest/value-types.html)
and the [Exec quoting and field codes](https://specifications.freedesktop.org/desktop-entry/latest/exec-variables.html).

These tests establish installer behavior with synthetic payloads. They do not
establish that a release binary starts, that native libraries are available, or
that the downloaded app has a valid signature. Native package installation and
launch checks are separate.
