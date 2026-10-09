# GG3 firmware realtime-status silence

This document records Ghost Gunner 3 firmware behavior that constrains DDGo's
controller-response watchdog. It is a code-inspection reference, not a proposed
watchdog design.

The firmware examined is
[`Defdist/grbl1v1g_GG3_GrBLDC3v0`](https://github.com/Defdist/grbl1v1g_GG3_GrBLDC3v0)
at commit
[`b42f8e461506e75b2d0b5d9014e64c323a49608d`](https://github.com/Defdist/grbl1v1g_GG3_GrBLDC3v0/tree/b42f8e461506e75b2d0b5d9014e64c323a49608d)
(`Updating latest firmware`). The findings below are specific to that revision
and should be revisited when the firmware changes.

Three behaviors must remain distinct:

1. Some commands legitimately defer realtime-status servicing for long enough
   to exceed DDGo's current two-second watchdog.
2. In a critical alarm, the firmware remains alive and processes `?`, but
   answers with critical-event feedback rather than a normal `<...>` status
   report.
3. EEPROM operations and synchronous diagnostic output create shorter
   communication blind spots that are real but are not presently expected to
   exceed two seconds during normal operation.

The first behavior is delayed servicing, the second is active communication in
a different response form, and the third is a lower-severity temporary
impediment. They are not equivalent.

## How GG3 realtime status is serviced

In `grblDD/serial.c`, the UART receive ISR intercepts an incoming realtime `?`
and sets `EXEC_STATUS_REPORT`. Receiving the byte does not itself transmit a
status report. The main firmware must later reach `protocol_execute_realtime()`
or `protocol_exec_rt_system()` before it services the pending flag.

Ordinary firmware paths reach those checkpoints frequently enough to appear
continuously responsive. Some special routines deliberately do not. The
following is therefore a legitimate firmware state:

```text
USB remains connected
controller continues executing correctly
? is received and EXEC_STATUS_REPORT is set
no <...> status response is transmitted yet
```

For those routines, the inference "no valid status report for about two
seconds means the controller stopped responding and therefore a physical
e-stop occurred" is false.

Relevant entry points are the serial receive ISR in `grblDD/serial.c` and
`protocol_execute_realtime()` / `protocol_exec_rt_system()` in
`grblDD/protocol.c`.

## Homing: the primary long-silence case

The full and single-axis homing commands are affected:

```text
$H
$HX
$HY
$HZ
```

Their relevant call path is:

```text
grblDD/system.c: system_execute_line()
    -> grblDD/motion_control.c: mc_homing_cycle()
    -> grblDD/limits.c: limits_go_home()
```

The important inner loop in `limits_go_home()` intentionally does not call
`protocol_execute_realtime()`. The source explains the omission directly:
"No time to run protocol_execute_realtime() in this loop." A `?` can therefore
be received and retained as a pending flag while no normal status is emitted
until the homing phase exits.

This silence can easily exceed DDGo's current two-second controller-response
timeout. Duration depends on the selected axis, starting position, and whether
the seek, locate/fine, debounce, or pull-off phase is executing. The examined
firmware contains these GG3 defaults:

| Setting | Default |
|---|---:|
| X travel | 86.5 mm |
| Y travel | 241.5 mm |
| Z travel | 78.5 mm |
| Homing seek rate | 2000 mm/min |
| Homing fine rate | 30 mm/min |
| Homing debounce | 1 ms |
| Homing pull-off | 0.5 mm |

Single-axis `$HX`, `$HY`, and `$HZ` use the same underlying homing routine as
full `$H`; they are not exempt from the blind period.

## GG3-specific leveling commands

`$L` and `$LS` are Ghost Gunner extensions rather than standard upstream Grbl
behavior. They combine ordinary homing blind periods with custom blocking
motion loops.

The relevant functions are:

- `grblDD/system.c`: `system_execute_line()`;
- `grblDD/motion_control.c`: `mc_autolevel_X()` and `mc_X_is_level()`;
- `grblDD/limits.c`: `limits_go_home()` and
  `limits_find_trip_delta_X1X2()`.

### `$L`

The approximate sequence is:

1. Home Z.
2. Repeat three times:
   1. home X;
   2. measure the X1/X2 limit-switch relationship;
   3. square or adjust X;
   4. home X again.

This includes repeated homing intervals and custom measurement/adjustment
loops that do not service realtime execution.

### `$LS`

The approximate sequence is:

1. Home Z.
2. Home X.
3. Measure the X1/X2 limit-switch relationship.
4. Store calibration data.
5. Home X again.

`limits_find_trip_delta_X1X2()` contains several motion loops without calls to
`protocol_execute_realtime()`. One relevant move is approximately 5 mm at the
default fine homing rate:

```text
30 mm/min = 0.5 mm/s
5 mm / 0.5 mm/s = approximately 10 seconds
```

That single finite movement can be status-silent for roughly ten seconds.
Repeated homing and other phases can extend the command further. Increasing a
global two-second timeout by a small fixed amount would therefore not remove
the underlying false inference.

### Robustness concern in the custom loops

Expected finite silence during a successful `$L` or `$LS` must be distinguished
from a potentially stuck operation. Some loops in
`limits_find_trip_delta_X1X2()` do not appear to service realtime reset/status
execution while waiting for an expected limit-switch transition. If that
transition never occurs, firmware may remain in the loop much longer than
normal or potentially indefinitely.

This is a firmware observation, not a confirmed DDGo failure. It does mean
DDGo cannot safely interpret all silence during leveling as acceptable without
limit. A future watchdog design must preserve the difference between expected
finite command-associated silence and a failed hardware/firmware progression.

## Critical alarms: communication without a normal heartbeat

Critical-alarm behavior in `grblDD/protocol.c` is a separate source of false
"unresponsive" classification. In `protocol_exec_rt_system()`, a critical
alarm enters a loop that waits for reset. If an incoming `?` sets
`EXEC_STATUS_REPORT` during that loop, firmware does not take the normal
`report_realtime_status()` path. It transmits a critical-event feedback message
and clears the pending status flag instead.

The controller is therefore connected, receiving `?`, executing the realtime
request, and transmitting a response, but that response is not a normal
`<...>` status report. DDGo currently treats only a successfully parsed
realtime status report as heartbeat proof. An ordinary Grbl alarm can
consequently be followed by `EStopSourceUnresponsive` even though the firmware
is actively communicating.

This differs fundamentally from homing and leveling: those commands defer
servicing the request, whereas the critical-alarm loop services it using a
different response form. Neither condition proves a physical e-stop.

Representative alarm-producing cases include:

- hard-limit alarm;
- soft-limit alarm;
- homing failure;
- probe failure.

The supported probe commands are `G38.2`, `G38.3`, `G38.4`, and `G38.5`.
Normal probe motion is not a long-silence case:
`grblDD/motion_control.c: mc_probe_cycle()` calls
`protocol_execute_realtime()` while waiting for the probe cycle. A probe
failure can, however, transition into the alarm behavior described above.

## Relevant commands checked that remain responsive

Code inspection did not find the same extended realtime-service gap in the
following relevant operations:

```text
G0, G1, G2, G3
G4
$J / jogging
G38.x during normal probe execution
M0, M2, M30
M3, M4, M5
```

This is the relevant set checked during this investigation, not an exhaustive
list of every G-code, M-code, or system command accepted by the firmware.

The ordinary paths remain responsive because planner and buffer waits call
realtime processing, probe-cycle waits call realtime processing, and suspend
and feed-hold handling continue realtime processing. Dwell execution reaches
realtime processing through `grblDD/nuts_bolts.c: delay_sec()` once per
`DWELL_TIME_STEP`; that constant is 50 ms in the examined firmware. Normal
`G38.x` execution must not be grouped with the long-silent homing and leveling
commands merely because a failed probe can later raise an alarm.

## Shorter communication blind spots

The following operations can temporarily impede communication, but they are
not presently expected to exceed DDGo's two-second watchdog under normal
conditions. They should be recorded as limitations without assigning them the
same severity as homing or GG3 leveling.

### EEPROM writes

The firmware EEPROM write path disables interrupts around EEPROM programming
operations and warns that serial receive data can be lost while writing.
Relevant commands include:

```text
G10
G28.1
G30.1
$N...=
$I=
$B=
$<setting>=...
$RST=...
```

Some paths synchronize the planner before writing. These are genuine receive
blind spots, but code inspection does not support claiming that they normally
produce multi-second silence. Relevant code spans the command handlers in
`grblDD/system.c`, the coordinate/settings persistence paths, and the EEPROM
write implementation in `grblDD/eeprom.c`.

### `$E` EEPROM dump

The custom `$E` command prints the full EEPROM contents synchronously from its
`system_execute_line()` path without inserting realtime-processing checkpoints
inside the print loop. At 115200 baud, the resulting output is not presently
expected to keep status processing blind for more than two seconds under
normal conditions. It remains a print-bound communication blind spot, not a
known long-silence command.

## Classification summary

| Command/state | Expected normal status silence >2 s? | Firmware alive? | Relevant behavior |
|---|---:|---:|---|
| `$H` | Yes | Yes | Homing loop defers realtime execution |
| `$HX` | Yes | Yes | Same underlying homing routine |
| `$HY` | Yes | Yes | Same underlying homing routine |
| `$HZ` | Yes | Yes | Same underlying homing routine |
| `$L` | Yes, potentially much longer | Yes | Homing plus custom blocking leveling loops |
| `$LS` | Yes, potentially much longer | Yes | Homing plus custom limit/calibration loops |
| `G38.x` normal probe | No | Yes | Probe loop services realtime execution |
| Critical alarm state | No normal `<...>` heartbeat | Yes | `?` receives critical-event feedback instead |
| `G4` dwell | No | Yes | Realtime serviced every 50 ms |
| EEPROM writes | Short blind spot | Yes | Interrupts temporarily disabled |
| `$E` | Short print-bound blind spot | Yes | Large synchronous output |

"No normal status response" in this table must not be shortened to "no
response": the critical-alarm case sends a response that DDGo does not
currently recognize as heartbeat proof.

## Implications for DDGo

This inspection establishes constraints, not the final watchdog implementation:

1. A global rule that "no valid status report for two seconds means e-stop" is
   incorrect for this firmware.
2. A modest global timeout increase is insufficient. Legitimate silence can be
   around ten seconds or longer and varies with command, axis, machine
   position, and motion phase.
3. DDGo will need to distinguish expected command-associated status silence,
   genuine controller unresponsiveness, and active alarm communication that is
   not a normal status report.
4. Expected silence cannot be unlimited because custom GG3 leveling loops may
   fail to reach their expected hardware transition.
5. USB disappearance and serial-transport loss remain separate disconnect
   conditions rather than status-heartbeat classifications.

These facts do not by themselves prescribe command exemptions, new timeout
values, alarm parsing policy, or another watchdog architecture. Those choices
require a separate design and implementation task.
