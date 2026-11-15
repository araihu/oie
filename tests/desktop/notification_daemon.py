"""Freedesktop notification daemon for the native smoke test's private session."""

import dbus
import dbus.service
from dbus.mainloop.glib import DBusGMainLoop
from gi.repository import GLib


INTERFACE = "org.freedesktop.Notifications"
PROBE = "org.oie.NotificationProbe"


class Notifications(dbus.service.Object):
    action = ""

    @dbus.service.method(INTERFACE, out_signature="as")
    def GetCapabilities(self):
        return ["actions", "body"]

    @dbus.service.method(INTERFACE, out_signature="ssss")
    def GetServerInformation(self):
        return "Oiê desktop smoke", "Oiê", "1", "1.2"

    @dbus.service.method(INTERFACE, in_signature="susssasa{sv}i", out_signature="u")
    def Notify(self, app, replaces, icon, title, body, actions, hints, timeout):
        if title == "Oiê notification smoke" and actions:
            self.action = actions[0]
        return dbus.UInt32(1)

    @dbus.service.signal(INTERFACE, signature="us")
    def ActionInvoked(self, notification_id, action):
        pass

    @dbus.service.method(PROBE, out_signature="b")
    def Click(self):
        if not self.action:
            return False
        self.ActionInvoked(1, self.action)
        self.action = ""
        return True


DBusGMainLoop(set_as_default=True)
name = dbus.service.BusName(INTERFACE, bus=dbus.SessionBus(), do_not_queue=True)
notifications = Notifications(name, "/org/freedesktop/Notifications")
GLib.MainLoop().run()
