# Linux serial-port access

DDGo reports all enumerated port metadata when ports appear, disappear, or the
user refreshes the list. Verified Ghost Gunner hardware in this project has
enumerated with USB VID/PID `2341:003e` and `2341:0043`; the physical GG3 used
for current testing reported `2341:0043`. DDGo accepts either identity as an
autoconnect candidate, with or without a USB serial descriptor. It still opens
the candidate and completes GRBL startup-banner and `$$` validation before
marking the controller connected.

Automatic connection remains limited to exactly one positively classified USB
candidate. Zero matches or multiple matches do not select a device. When
detailed USB enumeration is unavailable, DDGo's name-only fallback reports
ports with `USB=false` and empty IDs. Those ports remain available for manual
connection but are deliberately excluded from autoconnect because their names
do not positively identify a machine.

## Recognition compatibility

DDGo actively recognizes `2341:003e` and `2341:0043`. Older DDCut discovery
also included platform-specific paths that DDGo does not currently enable:

- macOS DDCut accepted `1A86:7523`, a generic CH340 USB-serial identity. DDGo
  requires physical Ghost Gunner validation before considering that broad ID,
  because unrelated CH340 devices could be probed or make discovery ambiguous.
- Windows DDCut matched the friendly name `Arduino Uno (COMx)`. DDGo's current
  port metadata does not expose that friendly name, and the name does not
  specifically identify a Ghost Gunner. A Windows fallback should be based on
  metadata observed from physical hardware if VID/PID discovery proves
  insufficient.

DDCut macOS also required a `/dev/cu.usbmodem...` path and a nonempty USB serial
number. DDGo intentionally does not copy those requirements: its discovery is
cross-platform, verified hardware may not reliably expose a serial descriptor,
and the protocol handshake validates the opened candidate.

## Persistent permissions

Running `chmod` on `/dev/ttyACM0` is only temporary: unplugging the controller
destroys that device node and Linux creates a new one with the system's normal
permissions. Inspect the actual owner and group first:

```sh
ls -l /dev/ttyACM0
```

If the port is owned by a serial-device group such as `dialout`, add your user
to the group shown by that command. On Debian and Ubuntu, for example:

```sh
sudo usermod -aG dialout "$USER"
```

Group names vary between distributions. Log out and back in (or restart the
user session) before testing again so the new membership is active.

An administrator can alternatively test and install controller-specific udev
rules. For the verified controller IDs, the rules are conceptually:

```udev
SUBSYSTEM=="tty", ATTRS{idVendor}=="2341", ATTRS{idProduct}=="003e", GROUP="dialout", MODE="0660"
SUBSYSTEM=="tty", ATTRS{idVendor}=="2341", ATTRS{idProduct}=="0043", GROUP="dialout", MODE="0660"
```

Install only the rule or rules appropriate for the hardware in use. Confirm the
attributes against the board's actual udev hierarchy and replace `dialout` with
the serial-device group used by the system before installing the rule.
Group-based `0660` access is preferred to world-writable `0666` access.
