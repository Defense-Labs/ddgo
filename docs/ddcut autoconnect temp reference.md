# DDCut autoconnect behavior

  ## Executive summary

  DDCut autoconnect is implemented by GhostConnector, not by the UI.

  Startup creates a dedicated connection thread. That thread repeatedly:

  1. Enumerates compatible USB-connected Ghost Gunners.
  2. Selects only the first discovered candidate.
  3. Opens its serial port.
  4. Waits for a GRBL startup banner.
  5. Waits for the GRBL startup phase to end.
  6. Sends $$ and waits for the normal GRBL response.
  7. Marks the connector connected.
  8. Every 200 ms, checks whether the selected device still appears in USB enumeration.

  There is no saved-port preference, no parallel probing, and no explicit protocol query used to
  identify a machine beyond the GRBL startup banner. Discovery is primarily USB metadata filtering;
  the serial handshake is the final validation.

  ———

  ## 1. Where autoconnect begins

  ### Startup call chain

  Electron app ready
    └─ createWindow()
        └─ DDController.Initialize()
            └─ ddcut.Initialize(callback)
                └─ InitializeService::Execute()
                    └─ DDCutDaemon::Initialize()
                        └─ GhostConnector::Initialize()
                            └─ starts GhostConnector::Thread_Connect

  Relevant locations:

  - UI/src/index.js:21
  - UI/src/Main/DDController.js:49
  - src/NodeWrapper.cpp:14
  - src/NAPI/Services/InitializeService.h:16
  - src/DDCutDaemon.cpp:55
  - src/Ghost/GhostConnector.cpp:8

  DDCutDaemon::Initialize() initializes settings first, then immediately creates the connector:

  SettingManager::GetInstance();
  m_pConnector = GhostConnector::Initialize();

  The connector starts its worker thread immediately. No UI event or user action is required.

  The UI separately begins reading the status 100 ms after the initialization callback completes:

  - UI/src/Main/DDController.js:75

  That UI timer observes autoconnect; it does not initiate it.

  ### Trigger type

  The trigger is a dedicated background thread:

  - src/Ghost/GhostConnector.cpp:37

  The loop sleeps 200 ms between iterations.

  ### Directly demonstrated

  - Autoconnect begins during daemon initialization.
  - It runs on a background thread.
  - It starts before the UI status polling timer.
  - It is not initiated by the Ghost chooser UI.

  ———

  ## 2. Candidate-device discovery

  GhostGunnerFinder::GetAvailableGhostGunners() is platform-specific:

  - src/Ghost/GhostGunnerFinder.h:5

  ### Windows

  File:

  - src/Ghost/GhostGunnerFinder_win.cpp:42

  Discovery uses Windows SetupDi APIs:

  SetupDiGetClassDevs(
      &GUID_DEVINTERFACE_SERENUM_BUS_ENUMERATOR,
      ...,
      DIGCF_PRESENT
  )

  It enumerates present serial devices with SetupDiEnumDeviceInfo.

  Filtering rule:

  const std::regex RXARDUINO("^Arduino Uno \\((.*)\\)$");

  Only devices whose friendly name matches:

  Arduino Uno (COMx)

  are accepted.

  The path becomes:

  \\.\COMx

  The device instance ID is also read. The substring after the final \ is retained as the Ghost
  GRunner serial number.

  Windows therefore uses:

  - present serial-device enumeration;
  - friendly name;
  - device instance ID;
  - no explicit VID/PID check in this code.

  Ordering is the order returned by SetupDiEnumDeviceInfo. The code does not sort it.

  ### macOS

  File:

  - src/Ghost/GhostGunnerFinder_osx.cpp:75

  The code:

  1. Uses IOKit serial-device matching.
  2. Reads IOCalloutDevice.
  3. Requires a path beginning with:

  /dev/cu.usbmodem

  4. Checks USB vendor/product IDs.

  Accepted IDs:

  VID 2341, PID 0043
  VID 1A86, PID 7523

  The USB serial number must be nonempty.

  Relevant code:

  - path filtering: src/Ghost/GhostGunnerFinder_osx.cpp:94
  - VID/PID filtering: src/Ghost/GhostGunnerFinder_osx.cpp:51

  Ordering is the order returned by the IOKit iterator. No explicit sort occurs.

  ### Linux

  File:

  - src/Ghost/GhostGunnerFinder_linux.cpp:52

  The code scans:

  /sys/bus/usb/devices/

  It identifies top-level USB device entries using regular expressions:

  - device: [0-9]+-[0-9]+
  - subnode: [0-9]+-[0-9]+:[0-9]+\.[0-9]+

  For each device it reads:

  idVendor
  idProduct
  serial

  Only this VID/PID is accepted:

  VID 2341
  PID 0043

  It then finds the associated tty entry and constructs:

  /dev/<tty-name>

  The devices are stored in:

  std::map<std::string, std::string>

  keyed by serial number. Consequently, Linux output is ordered lexicographically by serial number,
  not by /dev/tty* name or directory enumeration order.

  There is also a likely implementation quirk: the code checks if (nullptr != file) after creating
  fileName, but file is normally non-null inside the loop. The practical effect is that ordinary .
  and .. entries are skipped and other entries are accepted.

  ### Saved settings

  No saved port, COM number, USB serial number, registry preference, or last-successful-device
  setting is consulted by autoconnect.

  The SettingManager is initialized before the connector, but no connection-related setting is read
  by GhostConnector.

  ———

  ## 3. How DDCut decides that the port is correct

  ### Serial settings

  #### POSIX implementation

  File:

  - src/Ghost/GRBL/SerialConnection.cpp:28

  Settings:

  baud rate: 115200
  data bits: 8
  parity: none
  stop bits: 1

  The port is opened through Boost.Asio.

  #### Windows implementation

  File:

  - src/Ghost/GRBL/SerialConnection_windows.cpp:91

  Settings:

  baud rate: 115200
  data bits: 8
  parity: none
  stop bits: 1
  DTR: enabled
  RTS: disabled
  software flow control: disabled
  hardware CTS flow control: disabled

  Windows read timeout configuration:

  ReadIntervalTimeout = MAXDWORD
  ReadTotalTimeoutConstant = 0
  ReadTotalTimeoutMultiplier = 0
  WriteTotalTimeoutConstant = 0
  WriteTotalTimeoutMultiplier = 0

  ### Handshake

  Connection call chain:

  GhostConnector::SetSelectedGhostGunner()
    └─ GhostConnection::Connect(ghostGunner)
        └─ GhostConnection::Connect()
            └─ SerialConnection::Connect()
            └─ wait for GRBL startup banner
            └─ wait for startup state to clear
            └─ ExecuteCommand("$$")

  Relevant locations:

  - src/Ghost/GhostConnector.cpp:98
  - src/Ghost/GRBL/GhostConnection.cpp:59
  - src/Ghost/GRBL/GhostConnection.cpp:78

  After opening:

  m_pState->SetConnected(true);
  m_pState->SetStartup(true);
  m_pSerialConnection->FlushReads();

  The code then reads complete lines until it sees:

  Grbl <version>...

  The regular expression is:

  - src/Ghost/GRBL/Regex.h:8

  ^Grbl (\d+\.\d+).*

  Matching is case-insensitive.

  The captured version is passed to:

  m_protocol.SetVersion(...)

  The version determines whether later responses are interpreted as GRBL 1.0 or 1.1.

  ### Startup completion

  Seeing the banner is not quite sufficient. The code also waits until:

  started == true
  && state.IsStartup() == false

  GS_STARTUP is cleared after 10 seconds of inactivity:

  - src/Ghost/GRBL/ConnectionState.h:84
  - startup delay declaration: src/Ghost/GRBL/ConnectionState.h:182

  Every received line resets the idle timer:

  - src/Ghost/GRBL/SerialConnection.cpp:112

  Thus, successful startup normally requires:

  1. a matching Grbl x.y line;
  2. the startup state to remain quiet long enough to clear.

  ### Startup command

  Once startup completes, DDCut sends:

  $$

  This is sent through the ordinary command path:

  - src/Ghost/GRBL/GhostConnection.cpp:119
  - src/Ghost/GRBL/GhostConnection.cpp:220

  $$ is inserted into the command buffer and the normal response reader waits for the expected
  responses, including ok.

  ### Does merely opening the port count?

  No.

  Opening the port sets the internal ConnectionState connected flag, but GhostConnector does not
  report success until GhostConnection::Connect() returns. That requires the GRBL startup sequence
  and the $$ command path to complete successfully.

  However, the USB finder has already filtered candidates before the serial handshake. DDCut does
  not send a special machine-identification command or verify the Ghost Gunner serial number over
  the protocol.

  ### GRBL/custom-firmware behavior

  The code recognizes a generic GRBL banner and extracts only its numeric version. It does not
  require Ghost-specific text in the banner.

  Therefore, any device that:

  - passes the USB/serial discovery filter;
  - opens successfully;
  - emits a matching Grbl x.y startup line;
  - behaves sufficiently like GRBL during $$;

  can be accepted.

  That conclusion is directly demonstrated by the startup regex and connection logic.

  ———

  ## 4. Connection-attempt behavior

  ### Sequential or parallel

  Attempts are sequential, but only one candidate is attempted per scan.

  TryConnect() obtains the list and considers only:

  availableGhostGunners.front()

  - src/Ghost/GhostConnector.cpp:66

  It does not loop over all candidates.

  Important consequence:

  - If the first candidate is unsuitable, the second candidate is not attempted during that
    iteration.

  - The first candidate continues to be preferred on subsequent scans as long as it remains first.

  ### Retry interval

  The outer thread sleeps 200 ms after each iteration:

  - src/Ghost/GhostConnector.cpp:42

  If opening fails with FAILED_OPEN, the status is set to connecting, so the same candidate is
  retried approximately every 200 ms.

  If another error occurs, status becomes connectionFailed, but the outer loop still calls
  TryConnect() because the status is not connected. Therefore it is also retried approximately every
  200 ms.

  The difference is primarily the externally visible status.

  ### Port-open failure

  SerialConnection::Connect() throws GhostException(FAILED_OPEN, ...) when opening or configuring
  fails.

  Examples:

  - Boost.Asio open failure: src/Ghost/GRBL/SerialConnection.cpp:37
  - Windows CreateFile failure: src/Ghost/GRBL/SerialConnection_windows.cpp:95

  GhostConnector::SetSelectedGhostGunner() treats FAILED_OPEN specially:

  new_status = connecting;

  - src/Ghost/GhostConnector.cpp:114

  The source comment says this accommodates a newly plugged-in device whose OS handle is not
  immediately available.

  ### Port opens but wrong device

  There is no explicit “wrong device” response path.

  Possible behaviors:

  - If it emits no data, the startup loop eventually marks the state timed out after roughly two
    seconds, calls Reset(false), sends ?, waits up to roughly one second due to the write delay, and
    likely fails.

  - If it emits non-GRBL lines periodically, each line resets the idle timer, so the startup loop
    may continue indefinitely because started never becomes true.

  - If it emits a GRBL-looking banner but is not actually a Ghost Gunner, it may pass the handshake
    if it also responds sufficiently to $$.

  The non-GRBL periodic-output case is an important inferred edge case from the control flow.

  ### Explicit close before the next attempt

  For a failed open, the low-level connection object’s cleanup path calls Disconnect() through its
  destructor. On POSIX, Disconnect() stops the I/O context, joins the I/O thread, resets the serial
  port, and resets the context:

  - src/Ghost/GRBL/SerialConnection.cpp:61

  On Windows, failed setup paths explicitly call CloseHandle() before throwing:

  - src/Ghost/GRBL/SerialConnection_windows.cpp:101

  If the port opens but the higher-level handshake fails, the temporary GhostConnection is destroyed
  during stack unwinding. Its destructor attempts reset/disconnect if its internal state says
  connected:

  - src/Ghost/GRBL/GhostConnection.cpp:46

  So the intended behavior is to close before the next retry, although cleanup during an exception
  can itself encounter errors that are caught by the destructor.

  ———

  ## 5. State management

  ### Connector-level state

  The public connection status is:

  - src/Ghost/Status/ConnectionStatus.h:3

  notConnected     = 0
  connecting       = 1
  connected        = 2
  connectionFailed = -1

  Stored in:

  std::atomic<EGhostGunnerStatus> m_status;

  - src/Ghost/GhostConnector.h:38

  Transitions:

  initially: notConnected

  candidate selected:
      connecting

  successful GhostConnection::Connect():
      connected

  non-FAILED_OPEN exception:
      connectionFailed

  FAILED_OPEN exception:
      connecting

  unplug detected:
      notConnected

  The UI maps these values directly:

  - UI/src/Renderer/components/Status/Status.js:51

  ### Connection-level state

  Each GhostConnection owns a ConnectionState.

  Its flags include:

  GS_CONNECTED
  GS_STARTUP
  GS_TIMEOUT
  GS_ERROR
  GS_LOCKED
  GS_HOMING
  ...

  - src/Ghost/GRBL/ConnectionState.h:15

  The connector-level status and connection-level GS_CONNECTED are separate state systems.

  ### Retained data after success

  The connector retains:

  GhostConnection::Ptr m_pGhost;

  That connection retains:

  - selected path;
  - GhostGunner path and serial number;
  - serial connection;
  - GRBL protocol version;
  - GRBL settings;
  - command buffer;
  - machine status;
  - position/WCS state.

  The GhostGunner itself contains only:

  path
  serial number

  - src/Ghost/GhostGunner.h:5

  ### Last successful device

  There is no persistent last-device record.

  The current device remains in memory as m_pGhost, but it is not written to settings or reused
  explicitly on the next process startup.

  On the next startup, discovery order determines the first candidate again.

  ———

  ## 6. Interaction with normal machine communication

  ### When polling begins

  There are two distinct polling mechanisms.

  #### Connector/device-presence polling

  The connection worker runs every 200 ms:

  if not connected:
      discover and try to connect

  if connected:
      re-enumerate devices and check unplug status

  - src/Ghost/GhostConnector.cpp:43

  This is not GRBL status polling.

  #### UI connection-status polling

  The Electron main process queries the connector status every 100 ms:

  - UI/src/Main/DDController.js:75

  It calls:

  ddcut.GetGhostGunnerStatus()

  This only reports the connector enum.

  #### Machine-status polling

  Actual GRBL ? status requests occur when:

  - the renderer asks for machine status through Ghost::GetStatus;
  - Protocol::QueryStatus() determines the cached status is stale;
  - ReadResponse() determines a status request is needed;
  - program execution calls RefreshStatus().

  Relevant locations:

  - UI/src/Main/API/GhostGunnerAPI.js:63
  - src/Ghost/GRBL/Protocol/Protocol.cpp:142
  - src/Ghost/GRBL/GhostConnection.cpp:479

  The autoconnect worker itself does not continuously send ? after connection.

  ### Command queue activation

  The command buffer becomes active when the post-startup $$ command is sent:

  - src/Ghost/GRBL/GhostConnection.cpp:121
  - src/Ghost/GRBL/GhostWriter.cpp:129

  Normal command execution uses the same GhostConnection, Protocol, GhostWriter, and serial code as
  manual connection.

  ### Startup commands

  After the GRBL banner/startup phase:

  $$

  is sent.

  No automatic $X, ?, soft reset, or Ghost-specific identification command is sent as part of the
  successful startup path.

  A ? is sent only as part of timeout recovery through Reset(false).

  ### Manual connection

  Manual selection uses the same path:

  Ghost chooser
    └─ SelectGhostGunner(path, serial)
        └─ DDCutDaemon::SetSelectedGhostGunner()
            └─ GhostConnector::SetSelectedGhostGunner()
                └─ GhostConnection::Connect()

  Relevant locations:

  - UI chooser: UI/src/Renderer/components/Modals/GhostChooser/GhostChooser.js:32
  - IPC handler: UI/src/Main/API/GhostGunnerAPI.js:13
  - native wrapper: src/NodeWrapper.cpp:160
  - daemon forwarding: src/DDCutDaemon.cpp:215

  Autoconnect and manual selection share the same SetSelectedGhostGunner() and handshake
  implementation.

  ———

  ## 7. Failure and end conditions

  ### All candidates fail

  There is no explicit “all candidates exhausted” state.

  Because only the first candidate is tried, the implementation does not actually iterate through
  every candidate and then terminate.

  If no candidates are found:

  status remains whatever it was previously

  At startup that means notConnected.

  The worker continues scanning every 200 ms.

  If a candidate exists but connection fails:

  - FAILED_OPEN → connecting;
  - other connection failure → connectionFailed;
  - the worker continues retrying.

  ### User notification

  The UI displays:

  Not Connected
  Connecting
  Connected
  Connect Failed

  The status is pushed through:

  DD_UpdateGGStatus

  - UI/src/Main/DDController.js:35
  - UI/src/Renderer/components/Status/Status.js:51

  Connection exceptions are logged. The first exception from the worker loop is logged, but repeated
  failures are suppressed by previouslyFailed:

  - src/Ghost/GhostConnector.cpp:42

  previouslyFailed is never reset after a later success.

  ### Manual connection after failure

  Manual connection remains possible through the Ghost chooser. The chooser queries currently
  available devices synchronously and sends the selected path and serial number.

  The chooser is disabled while milling, not merely because autoconnect is active:

  - UI/src/Renderer/components/BottomToolbar/BottomToolbar.js:46

  ———

  ## 8. Disconnect/reconnect behavior

  ### Established connection disappears

  CheckUnplugged() re-enumerates devices and compares their paths with the active connection path:

  - src/Ghost/GhostConnector.cpp:78

  If the path is absent:

  m_status = notConnected
  m_pGhost->Disconnect()
  m_pGhost = nullptr

  On the next worker iteration, autoconnect runs again.

  Approximate timing:

  up to 200 ms for unplug detection
  plus up to 200 ms before the next connection attempt

  In normal operation, reconnect therefore begins within roughly 400 ms, excluding enumeration and
  handshake time.

  ### Timing differences

  Startup autoconnect and post-unplug reconnect use the same worker loop and same 200 ms delay.

  There is no separate reconnect backoff.

  ### Other communication failures

  A write failure, GRBL timeout, alarm, or protocol error does not directly set the connector status
  to disconnected and does not directly invoke TryConnect().

  The connector’s reconnection logic is based on USB/device enumeration, not on all serial or
  protocol failures.

  This means an established connection can remain connector-level connected even if higher-level
  communication has failed, unless another path explicitly disconnects it or the device disappears
  from enumeration.

  ### GhostConnection::Reconnect()

  There is a separate method:

  - src/Ghost/GRBL/GhostConnection.cpp:188

  It performs:

  if internally connected:
      Disconnect()
  Connect()

  This is not the connector’s automatic reconnect path. It is used by other machine-recovery logic,
  not by GhostConnector::Thread_Connect().

  ———

  # A. Complete autoconnect pseudocode

  application startup:

      Electron creates main window

      DDController.Initialize():
          call native ddcut.Initialize(callback)

      native InitializeService:
          run DDCutDaemon.Initialize()

      DDCutDaemon.Initialize():
          initialize SettingManager
          create GhostConnector
          create GhostConnector's connection thread
          create MillingManager
          create FirmwareUpdater

  GhostConnector.Initialize():
      construct connector
          status = notConnected
          selected connection = null
          shutdown = false

      start Thread_Connect(connector)

  connection thread:

      previouslyFailed = false

      while shutdown is false:

          try:
              if connector.status != connected:
                  TryConnect()

              if connector.status == connected:
                  CheckUnplugged()

          catch std::exception:
              if previouslyFailed is false:
                  log the failure
                  previouslyFailed = true

          sleep 200 ms


  TryConnect():

      enumerate available Ghost Gunners using platform finder

      if list is not empty:
          candidate = first item in list

          if candidate is not already selected:
              SetSelectedGhostGunner(candidate)


  SetSelectedGhostGunner(candidate):

      if candidate is already selected:
          return false

      if current status != connectionFailed:
          status = connecting

      try:
          create GhostConnection for candidate

          open serial port
          configure 115200, 8N1
          on Windows enable DTR

          mark internal connection state connected
          mark internal startup state true

          flush existing serial input

          started = false

          loop:
              read one complete serial line

              if a line is available:
                  reset idle timer

                  if line matches:
                      "Grbl <version>..."
                      save GRBL version
                      started = true

                  continue

              update connection state

              if started and startup flag is clear:
                  leave loop

              if state timed out:
                  perform Reset(false):
                      read/flush pending input
                      send '?'
                      wait for response

                  if reset fails:
                      throw ESTOP_PUSHED

              sleep 50 ms

          flush serial input

          execute injected "$$"
          wait through normal command-response handling

          store resulting GhostConnection
          status = connected
          return true

      catch exception:
          log failure

          if exception type == FAILED_OPEN:
              status = connecting
          else:
              status = connectionFailed

          return false


  CheckUnplugged():

      enumerate available Ghost Gunners

      if active connection path is still present:
          return

      status = notConnected
      disconnect active GhostConnection
      clear active GhostConnection

      next worker iteration retries discovery

  ———

  # B. Compact state machine

                   no device found
          ┌─────────────────────────────┐
          │                             │
          ▼                             │
    NOT_CONNECTED ──candidate found──► CONNECTING
          ▲                              │
          │                              │ successful banner,
          │                              │ startup completion,
          │                              │ "$$" succeeds
          │                              ▼
          │                         CONNECTED
          │                              │
          │                              │ device path disappears
          │                              ▼
          └────────────────────── NOT_CONNECTED

  CONNECTING ──FAILED_OPEN──────────────► CONNECTING
  CONNECTING ──other failure────────────► CONNECTION_FAILED
  CONNECTION_FAILED ──worker retry──────► CONNECTING
  CONNECTED ──protocol failure──────────► usually remains CONNECTED

  The CONNECTION_FAILED state is not terminal. The worker continues attempting connection.

  ———

  # C. Edge cases and quirks

  1. Only the first discovered candidate is attempted. Later candidates are not tried if the first
     one fails.

  2. Candidate ordering differs by operating system:
      - Windows: SetupDi enumeration order.
      - macOS: IOKit iterator order.
      - Linux: serial-number-sorted std::map.

  3. No remembered port or remembered machine is preferred.
  4. Windows filters on friendly name Arduino Uno (COMx), not VID/PID.
  5. macOS requires /dev/cu.usbmodem..., which excludes other serial path forms.
  6. Linux recognizes only VID 2341 / PID 0043; macOS recognizes an additional 1A86 / 7523 pair.
  7. USB serial numbers are retained as metadata but are not used to validate the serial protocol
     connection.

  8. A generic GRBL device may be accepted if it passes the platform discovery filter.
  9. A device that continuously emits non-GRBL lines can keep the startup loop alive indefinitely
     because each line resets the idle timer while started remains false.

  10. Startup waits for the startup flag to clear after 10 seconds of inactivity, not merely for the
     banner.

  11. The initial handshake sends no explicit Ghost Gunner identification command.
  12. $$ is the first normal command sent after startup.
  13. The connector’s connected state and the lower-level ConnectionState::GS_CONNECTED flag are
     separate.

  14. Serial/protocol failures do not necessarily cause connector-level reconnection.
  15. Automatic unplug detection checks device presence by path, not by serial number.
  16. Reconnect after unplug uses the same approximately 200 ms worker cadence as startup.
  17. previouslyFailed suppresses later connection-error log messages and is never reset.
  18. The connector does not have an explicit exhausted-candidates condition.
  19. On Windows, DTR is enabled during serial setup; this may affect reset/startup behavior in a
     replacement.

  20. The POSIX implementation uses an asynchronous read thread, while the Windows implementation
     uses synchronous ReadFile.

  21. Failed connection cleanup depends partly on destructors and exception unwinding.
  22. The autoconnect worker may race with UI/manual selection because both can call the same
     selection path; there is no visible connector-level mutex around m_pGhost and m_status.

  23. The UI status timer starts only after native initialization completes, but the connection
     worker may already be attempting connection before the first UI status query.

  ———

  # D. Most important behaviors to compare with a modern replacement

  1. Whether discovery uses platform USB metadata, serial-port names, or both.
  2. Exact accepted VID/PID and descriptor rules for each operating system.
  3. Candidate ordering.
  4. Whether only the first candidate is attempted or all candidates are tried.
  5. Whether a failed first candidate blocks or delays discovery of later candidates.
  6. Serial configuration: 115200 baud, 8N1, and Windows DTR behavior.
  7. Required startup banner format: Grbl <major>.<minor>.
  8. Whether startup requires the 10-second idle transition after the banner.
  9. The exact post-startup command: $$.
  10. Whether successful connection requires command-response completion or merely an open port.
  11. Timeout and retry cadence:
      - approximately 2-second connection-state timeout;
      - approximately 200 ms outer retry interval;
      - approximately 50 ms startup-loop sleep;
      - approximately 1-second write delay for timeout recovery ?.

  12. Whether a wrong device that emits data can hang startup detection.
  13. Whether the last successful device is remembered.
  14. Whether reconnect is driven by USB disappearance, serial errors, or both.
  15. Whether unplug detection compares path, USB serial number, or device identity.
  16. Whether connector status is kept separate from internal serial/GRBL state.
  17. Whether $$ is sent automatically after connection.
  18. Whether normal status polling begins immediately or only when the UI requests machine status.
  19. Whether manual selection shares the same handshake and state transitions.
  20. Whether connection failures remain visible as Connect Failed or quickly return to Connecting.
  21. Whether repeated failures are logged individually or suppressed.
  22. Whether failed attempts explicitly close the port before retrying.
  23. Whether a modern implementation preserves the distinction between:
      - no device discovered;
      - port opening;
      - GRBL startup detected;
      - startup completed;
      - post-startup initialization;
      - fully connected.

