#!/usr/bin/env python3
"""Development-server smoke check; not an Oie protocol client implementation."""
import argparse
import base64
import socket
import ssl
import time
import xml.etree.ElementTree as ET
from xml.sax.saxutils import escape

S = "http://etherx.jabber.org/streams"
TLS = "urn:ietf:params:xml:ns:xmpp-tls"
SASL = "urn:ietf:params:xml:ns:xmpp-sasl"


class Client:
    def __init__(self, username, password, endpoint, ca_file):
        self.socket = socket.create_connection(endpoint, timeout=8)
        self.socket.settimeout(8)
        self.restart()
        self.receive(f"{{{S}}}features")
        self.send(f'<starttls xmlns="{TLS}"/>')
        self.receive(f"{{{TLS}}}proceed")
        context = ssl.create_default_context(cafile=ca_file)
        self.socket = context.wrap_socket(self.socket, server_hostname="localhost")
        self.restart()
        self.receive(f"{{{S}}}features")
        token = base64.b64encode(("\0" + username + "\0" + password).encode()).decode()
        self.send(f'<auth xmlns="{SASL}" mechanism="PLAIN">{token}</auth>')
        self.receive(f"{{{SASL}}}success")
        self.restart()
        self.receive(f"{{{S}}}features")
        self.send('<iq type="set" id="bind"><bind xmlns="urn:ietf:params:xml:ns:xmpp-bind">'
                  '<resource>oie-smoke</resource></bind></iq>')
        self.receive("{jabber:client}iq", "bind")
        self.send("<presence/>")

    def restart(self):
        self.parser = ET.XMLPullParser(events=("end",))
        self.send(f'<stream:stream to="localhost" xmlns="jabber:client" xmlns:stream="{S}" version="1.0">')

    def send(self, text):
        self.socket.sendall(text.encode())

    def receive(self, tag, stanza_id=None):
        while True:
            for _, element in self.parser.read_events():
                if element.tag == tag and (stanza_id is None or element.get("id") == stanza_id):
                    if element.get("type") == "error":
                        raise RuntimeError(ET.tostring(element).decode())
                    return element
                if element.tag in (f"{{{SASL}}}failure", f"{{{S}}}error"):
                    raise RuntimeError(ET.tostring(element).decode())
            chunk = self.socket.recv(65536)
            if not chunk:
                raise RuntimeError("XMPP connection closed")
            self.parser.feed(chunk)

    def close(self):
        self.socket.close()


def main():
    args = argparse.ArgumentParser()
    args.add_argument("--host", default="127.0.0.1", help="TCP host for the localhost XMPP domain")
    args.add_argument("--port", type=int, default=5222)
    args.add_argument("--ca-file", required=True)
    options = args.parse_args()
    endpoint = (options.host, options.port)
    alice = Client("alice", "alice-local-only", endpoint, options.ca_file)
    bob = Client("bob", "bob-local-only", endpoint, options.ca_file)
    try:
        alice.send('<iq type="get" id="disco" to="localhost"><query xmlns="http://jabber.org/protocol/disco#info"/></iq>')
        info = alice.receive("{jabber:client}iq", "disco")
        features = {e.get("var") for e in info.iter() if e.tag.endswith("}feature")}
        assert "urn:xmpp:carbons:2" in features, features
        # MAM is advertised on the user's archive, not necessarily the server.
        alice.send('<iq type="get" id="archive-disco" to="alice@localhost"><query xmlns="http://jabber.org/protocol/disco#info"/></iq>')
        archive = alice.receive("{jabber:client}iq", "archive-disco")
        archive_features = {e.get("var") for e in archive.iter() if e.tag.endswith("}feature")}
        assert "urn:xmpp:mam:2" in archive_features, archive_features
        marker = "oie-smoke-" + str(time.time_ns())
        alice.send('<message to="bob@localhost/oie-smoke" type="chat" id="' + marker + '"><body>' +
                   escape(marker) + '</body></message>')
        received = bob.receive("{jabber:client}message", marker)
        assert received.findtext("{jabber:client}body") == marker
        print("PASS: verified TLS, Alice/Bob authentication, MAM/carbons discovery, and message delivery")
    finally:
        alice.close()
        bob.close()


if __name__ == "__main__":
    main()
