# DDCut e-stop behavior: source reference
 
  This report is based on direct inspection of the repository source. It distinguishes:
 
  - Demonstrated: directly shown by source.
  - Inferred: follows from surrounding control flow but depends on timing or runtime conditions.
  - Unknown: requires Ghost Gunner firmware or hardware information.
 
  No files were modified.
 
  ## Executive summary
 
  DDCut has two separate emergency-stop mechanisms:
 
  1. Hardware e-stop handling
      - A received ALARM:50 is interpreted as the physical emergency-stop alarm.
      - A communication timeout during normal response processing is also converted into alarm code
        50.
 
      - The source explicitly says that DDCut assumes timeouts are e-stops because they are
        difficult to distinguish.
 
  2. Software emergency stop
      - GhostConnection::EmergencyStop() sends realtime byte 0x18 / Ctrl-X.
      - It sets a transient m_emergencyStop flag.
      - The response reader throws SOFTWARE_ESTOP.
      - This becomes DDCut alarm code 52.
 
  DDCut does not parse a dedicated e-stop pin field such as Pn:. It also does not have a dedicated
  GS_ESTOP state. Physical e-stop is represented indirectly through retained alarm code 50.
 
  The physical machine's immediate stopping behavior is not implemented in this repository. That
  behavior is presumed to occur in hardware or firmware. DDCut discovers it afterward through an
  alarm response or lack of serial responses.
 
  ———
 
 # 1. All e-stop-related concepts
 
  ## Explicit e-stop symbols
 
   Symbol                             Location              Meaning
  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━  ━━━━━━━━━━━━━━━━━━━━  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
   ALARM_CODE_ESTOP = 50              src/Ghost/Status/     Physical e-stop alarm code
                                      MillingError.h:6
  ─────────────────────────────────  ────────────────────  ─────────────────────────────────────────
   ALARM_CODE_SOFT_ESTOP = 52         src/Ghost/Status/     Software e-stop alarm code
                                      MillingError.h:8
  ─────────────────────────────────  ────────────────────  ─────────────────────────────────────────
   GhostException::ESTOP_PUSHED       src/Ghost/            Hardware-timeout/e-stop exception
                                      GhostException.h:5
                                      6
  ─────────────────────────────────  ────────────────────  ─────────────────────────────────────────
   GhostException::SOFTWARE_ESTOP     src/Ghost/            Software-stop exception
                                      GhostException.h:5
                                      5
  ─────────────────────────────────  ────────────────────  ─────────────────────────────────────────
   MillingError::IsEStop()            src/Ghost/Status/     True only for alarm ID 50
                                      MillingError.h:49-
                                      52
  ─────────────────────────────────  ────────────────────  ─────────────────────────────────────────
   GhostConnection::EstopEngaged()    src/Ghost/GRBL/       True only when retained alarm equals 50
                                      GhostConnection.h:
                                      120
  ─────────────────────────────────  ────────────────────  ─────────────────────────────────────────
   m_emergencyStop                    src/Ghost/GRBL/       Transient software-stop flag
                                      GhostConnection.h:
                                      166
 
  ## Related concepts that are not independently e-stop detection
 
  The following occur in the code but are not themselves proof of physical e-stop:
 
  - GS_ERROR
  - GS_LOCKED
  - GS_TIMEOUT
  - ALARM_CODE_TIMEOUT = 51
  - ALARM_CODE_MACHINE_LOCKED = 53
  - GRBL HOLD
  - GRBL DOOR
  - hard-limit alarm
  - soft-limit alarm
  - homing alarm
  - probe alarm
  - feed hold (!)
  - feed resume (~)
  - jog cancellation (0x85)
  - ordinary soft reset / Ctrl-X
 
  Relevant files:
 
  - src/Ghost/Status/MillingError.h:6-52
  - src/Ghost/Status/MillingError.cpp:53-121
  - src/Ghost/GhostException.h:22-57
  - src/Ghost/GRBL/ConnectionState.h:15-28
  - src/Ghost/GRBL/Status/LimitSwitchState.h:6-74
  - src/Ghost/GRBL/Mock_GRBL_Status.h:14-22
 
  ———
 
 # 2. Actual source of the e-stop signal
 
  ## 2.1 Explicit serial alarm: ALARM:50
 
  The production protocol parser handles incoming serial lines in:
 
  - src/Ghost/GRBL/Protocol/Protocol.cpp:6-116
 
  The relevant branch is:
 
  if (StringUtil::StartsWith(line, "ALARM:")) {
      int alarm_num = -1;
 
      if (m_version == GRBLVersion::GRBL1_1) {
          alarm_num = std::stoi(line.substr(6));
      }
 
      ...
 
      m_pState->SetAlarm(alarm_num);
      ...
  }
 
  For GRBL 1.1:
 
  ALARM:50
 
  becomes numeric alarm ID 50.
 
  ConnectionState::SetAlarm() stores the alarm and sets generic error/locked flags:
 
  - src/Ghost/GRBL/ConnectionState.h:122-147
 
  The parser then throws a generic GhostException::ALARM.
 
  The e-stop meaning of numeric code 50 is defined here:
 
  - src/Ghost/Status/MillingError.h:6
  - src/Ghost/Status/MillingError.h:49-52
  - src/Ghost/Status/MillingError.cpp:63-66
 
  The user-facing description is:
 
  Job was aborted due to pressing the emergency stop button.
  To reconnect, please twist the red emergency stop switch clockwise until it pops out.
  After resetting the button, press 'OK' to start program from the very beginning.
 
  ### Directly demonstrated
 
  For GRBL 1.1, DDCut treats a received:
 
  ALARM:50
 
  as the physical e-stop indication.
 
  ### GRBL 1.0 caveat
 
  For GRBL 1.0, the parser does not convert the alarm suffix to a numeric ID:
 
  if (m_version == GRBLVersion::GRBL1_0) {
      description = line.substr(6);
  }
 
  The numeric alarm remains -1.
 
  Therefore, the source does not demonstrate that a GRBL 1.0 alarm line can become
  MillingError::IsEStop() == true.
 
  Relevant code:
 
  - src/Ghost/GRBL/Protocol/Protocol.cpp:89-100
 
  ———
 
 ## 2.2 Communication timeout treated as hardware e-stop
 
  GhostConnection::ReadResponse() handles missing serial responses:
 
  - src/Ghost/GRBL/GhostConnection.cpp:479-518
 
  The relevant logic is:
 
  m_pState->UpdateState();
 
  if (m_pState->IsTimedOut()) {
      if (DDCutDaemon::GetInstance().GetManualOperationFlag()) {
          DD_LOG("Ignore timeout in manual operation mode.");
          return;
      }
 
      m_pState->SetAlarm(ALARM_CODE_ESTOP);
      throw GhostException(GhostException::ESTOP_PUSHED);
  }
 
  The source comment explicitly states:
 
  For now, we'll assume all timeouts are E-Stops since they're difficult to differentiate
 
  ConnectionState::UpdateState() marks the connection timed out after:
 
  - approximately 2 seconds normally;
  - approximately 25 seconds while homing.
 
  Source:
 
  - src/Ghost/GRBL/ConnectionState.h:84-95
 
  ### Directly demonstrated
 
  During normal/non-manual response handling:
 
  no serial response
      -> timeout
      -> alarm code 50
      -> ESTOP_PUSHED
 
  ### Important consequence
 
  A cable failure, stalled firmware, serial-driver failure, or other non-response can be classified
  as a physical e-stop.
 
  That is a demonstrated conflation in the implementation.
 
  ### Manual-operation exception
 
  When the manual-operation flag is enabled, timeout is ignored:
 
  if (GetManualOperationFlag()) {
      return;
  }
 
  Source:
 
  - src/Ghost/GRBL/GhostConnection.cpp:498-502
 
  This means timeout-based e-stop detection is disabled during manual operation.
 
  An explicit ALARM:50 can still be parsed during manual operation.
 
  ———
 
 # 3. No e-stop pin or realtime-status parser
 
  DDCut parses GRBL realtime status reports in:
 
  - src/Ghost/GRBL/Status/RealTimeStatus.h:15-71
  - src/Ghost/GRBL/Status/RealTimeStatus.h:74-126
 
  The parser extracts:
 
  - machine state;
  - machine substate;
  - position;
  - buffer state;
  - line number;
  - limit/probe state;
  - work coordinates;
  - feed/spindle rates.
 
  Optional fields are handled only for:
 
  W
  FS
  F
 
  Source:
 
  - src/Ghost/GRBL/Status/RealTimeStatus.h:58-69
 
  There is no handling for:
 
  Pn:
 
  or any other explicit e-stop pin field.
 
  LimitSwitchState::Parse() accepts exactly four characters:
 
  const auto probe = buffer[0] == 'P';
 
  return LimitSwitchState(
      probe,
      buffer[1] == 'X',
      buffer[2] == 'Y',
      buffer[3] == 'Z'
  );
 
  Source:
 
  - src/Ghost/GRBL/Status/LimitSwitchState.h:18-27
 
  The P position is probe, not e-stop.
 
  ### Conclusion
 
  Demonstrated: DDCut does not detect physical e-stop from a GRBL pin-state field or realtime-status
  field.
 
  ———
 
 # 4. Complete data flow
 
  ## 4.1 Hardware e-stop reported as ALARM:50
 
  firmware emits "ALARM:50"
      ↓
  SerialConnection::ReadLine()
      ↓
  GhostConnection::ReadResponse()
      ↓
  Protocol::ProcessResponse("ALARM:50")
      ↓
  ConnectionState::SetAlarm(50)
      ↓
  Protocol throws GhostException::ALARM
      ↓
  MillingManager / GetMillingStatus obtains retained alarm
      ↓
  ErrorCodes::GetAlarm(50)
      ↓
  MillingError::IsEStop() == true
      ↓
  GhostConnection::EstopEngaged() == true
      ↓
  NodeWrapper::GetGhostGunnerStatus()
      ↓
  connection_status may become connectionFailed (-1)
      ↓
  DDController forwards status to renderer
 
  Relevant locations:
 
  - serial line reading: src/Ghost/GRBL/SerialConnection.cpp:112-145
  - response reading: src/Ghost/GRBL/GhostConnection.cpp:479-489
  - alarm parser: src/Ghost/GRBL/Protocol/Protocol.cpp:89-109
  - retained alarm: src/Ghost/GRBL/ConnectionState.h:122-147
  - error conversion: src/Ghost/Status/MillingError.cpp:97-121
  - e-stop predicate: src/Ghost/Status/MillingError.h:49-52
  - connection predicate: src/Ghost/GRBL/GhostConnection.h:117-120
  - API projection: src/NodeWrapper.cpp:90-111
  - UI status polling: UI/src/Main/DDController.js:35-45, 75-78
 
  ## 4.2 Hardware e-stop inferred from timeout
 
  physical e-stop or other non-response condition
      ↓
  ReadResponse() receives no complete line
      ↓
  ConnectionState::UpdateState()
      ↓
  GS_TIMEOUT becomes set
      ↓
  ReadResponse() sees timeout
      ↓
  ConnectionState::SetAlarm(50)
      ↓
  throw GhostException::ESTOP_PUSHED
      ↓
  MillingManager catches exception
      ↓
  later CheckForError()/GetMillingStatus()
      ↓
  MillingError alarm 50
      ↓
  UI receives failure/error
 
  Relevant locations:
 
  - timeout update: src/Ghost/GRBL/ConnectionState.h:84-95
  - timeout conversion: src/Ghost/GRBL/GhostConnection.cpp:495-509
  - milling exception handling: src/Ghost/MillingManager.cpp:78-123
  - deferred error handling: src/Ghost/MillingManager.cpp:125-163
 
  ## 4.3 Software emergency stop
 
  user invokes DDCut stop
      ↓
  Jobs::EmergencyStop IPC
      ↓
  NodeWrapper::EmergencyStop()
      ↓
  DDCutDaemon::EmergencyStop()
      ↓
  GhostConnection::EmergencyStop()
      ↓
  m_emergencyStop = true
      ↓
  send realtime byte 0x18 / Ctrl-X
      ↓
  Reset(false), which sends '?'
      ↓
  ReadResponse() sees m_emergencyStop on no-data branch
      ↓
  throw GhostException::SOFTWARE_ESTOP
      ↓
  GhostErrorHandler::GetError()
      ↓
  ErrorCodes::GetAlarm(52)
      ↓
  software e-stop error returned to UI
 
  Relevant locations:
 
  - UI IPC: UI/src/Main/API/JobsAPI.js:108-110
  - native wrapper: src/NodeWrapper.cpp:451-455
  - daemon: src/DDCutDaemon.cpp:863-875
  - connection stop: src/Ghost/GRBL/GhostConnection.cpp:204-218
  - response-reader branch: src/Ghost/GRBL/GhostConnection.cpp:490-493
  - error translation: src/Ghost/GhostErrorHandler.cpp:5-15
  - software-stop message: src/Ghost/Status/MillingError.cpp:63-66
 
  ———
 
  # 5. How fresh e-stop state is obtained
 
  There are three separate polling systems.
 
  ## 5.1 USB presence polling
 
  GhostConnector::Thread_Connect() runs approximately every 200 ms:
 
  - src/Ghost/GhostConnector.cpp:37-64
 
  When connected, it calls:
 
  CheckUnplugged();
 
  CheckUnplugged() only re-enumerates Ghost Gunners and checks whether the current serial path still
  exists:
 
  - src/Ghost/GhostConnector.cpp:78-96
 
  This detects unplugging, not e-stop.
 
  ## 5.2 UI connection-status polling
 
  The Electron main process calls:
 
  ddcut.GetGhostGunnerStatus()
 
  every 100 ms:
 
  - UI/src/Main/DDController.js:35-45
  - UI/src/Main/DDController.js:75-78
 
  GetGhostGunnerStatus() calls:
 
  DDCutDaemon::GetInstance().GetMillingStatus(false)
 
  and then reads connector status:
 
  - src/NodeWrapper.cpp:90-111
 
  When milling is not in progress, GetMillingStatus() invokes:
 
  CheckForError(pConnection);
 
  - src/Ghost/MillingManager.cpp:165-171
 
  CheckForError() calls:
 
  pConnection->ReadResponse(false);
 
  - src/Ghost/MillingManager.cpp:125-163
 
  Therefore, the 100 ms UI connection-status timer can indirectly cause response/error checking
  while idle.
 
  It is still not a dedicated e-stop monitor owned by the daemon.
 
 ## 5.3 GRBL realtime status polling
 
  DDCutDaemon::GetStatus() calls:
 
  GhostConnection::QueryStatus()
 
  - src/DDCutDaemon.cpp:711-730
 
  Protocol::QueryStatus() sends ? only when cached status is not recent:
 
  - src/Ghost/GRBL/Protocol/Protocol.cpp:142-169
 
  Cached status is considered recent for approximately 200 ms:
 
  - src/Ghost/GRBL/ConnectionState.h:149-163
 
  The repository's explicit recurring GetStatus() UI caller is the Shuttle/Operations component:
 
  - UI/src/Renderer/components/Modals/Shuttle/Operations.js:299-316
 
  It sends:
 
  ipcRenderer.send('Ghost::GetStatus');
 
  every 200 ms while mounted.
 
  ### Important distinction
 
  The main 100 ms connection-status timer and the Shuttle 200 ms machine-status timer are different.
 
  - The 100 ms timer queries connection/milling status.
  - The 200 ms Shuttle timer queries realtime machine status.
  - The 200 ms connector loop checks USB presence.
  - None of these are the same system.
 
  ———
 
 # 6. Behavior by operating state
 
  ## Idle
 
  ### Demonstrated
 
  The connector itself only checks USB presence. It does not continuously send ?.
 
  ### Inferred
 
  While the main UI process is active, its 100 ms GetGhostGunnerStatus() calls cause
  GetMillingStatus(false) and, when not milling, CheckForError() calls ReadResponse(false). That can
  eventually observe an explicit alarm or set timeout state.
 
  If the Shuttle status component is mounted, it also sends ? approximately every 200 ms.
 
  If no relevant API caller is active, there is no daemon-owned universal e-stop polling bound.
 
  ## Manual jogging
 
  Before jogging, Jog():
 
  1. reads settings;
  2. sends injected ?;
  3. computes the jog command;
  4. sends $J=....
 
  - src/Ghost/GRBL/Jogging/JogManager.cpp:7-54
 
  The jog command uses the normal command/response path.
 
  The manual-operation flag is enabled by the Shuttle UI:
 
  - UI/src/Renderer/components/Modals/Shuttle/Operations.js:305-312
 
  While that flag is true, timeout-based e-stop conversion is suppressed:
 
  - src/Ghost/GRBL/GhostConnection.cpp:498-502
 
  Explicit ALARM:50 remains parseable through the common protocol handler.
 
  ## Program running
 
  Program execution call chain:
 
  DDCutDaemon::StartMilling()
      -> MillingManager::MillOperationAsync()
      -> MillingManager::RunGCode()
      -> GhostConnection::ExecuteProgram()
      -> GhostConnection::ExecuteLine()
      -> ReadResponse()
 
  Relevant locations:
 
  - src/DDCutDaemon.cpp:836-841
  - src/Ghost/MillingManager.cpp:21-41, 78-123
  - src/Ghost/GRBL/GhostConnection.cpp:246-296
  - src/Ghost/GRBL/GhostConnection.cpp:314-367
 
  Detection is coupled to command-response processing. ReadResponse():
 
  - reads incoming lines;
  - parses alarms;
  - updates timeout state;
  - may send ?;
  - throws on timeout.
 
  There is no independent fixed-rate e-stop worker during programs.
 
 ## Program paused
 
  Feed hold sends:
 
  !
 
  Resume sends:
 
  ~
 
  - UI/src/Renderer/containers/Milling/Milling.js:786-795
 
  Feed hold is not an e-stop state and does not set alarm code 50.
 
  There is no separate e-stop monitor while paused. Detection depends on whatever response/status
  activity continues.
 
  ## Waiting for a command response
 
  This is the strongest timeout-detection path.
 
  ReadResponse(true) remains active until the command buffer is drained:
 
  - src/Ghost/GRBL/GhostConnection.cpp:479-518
 
  If no response arrives for approximately two seconds, normal operation treats that as hardware e-
  stop.
 
  ## Startup / connecting
 
  The connection handshake:
 
  open serial
      -> configure serial
      -> mark internal connection connected
      -> wait for "Grbl x.y"
      -> wait for startup state to clear
      -> send "$$"
      -> finish connection
 
  - src/Ghost/GRBL/GhostConnection.cpp:78-122
 
  There is no explicit e-stop query or safety-state validation during handshake.
 
  ———
 
# 7. Practical detection latency
 
  ## Explicit ALARM:50
 
  If the firmware emits ALARM:50:
 
  firmware emits line
      -> serial transport delivers line
      -> active ReadLine() obtains line
      -> Protocol::ProcessResponse()
 
  The source has no fixed additional polling interval for this path.
 
  On POSIX, serial input is continuously accumulated by the Boost.Asio read callback:
 
  - src/Ghost/GRBL/SerialConnection.cpp:181-231
 
  On Windows, ReadLine() reads synchronously:
 
  - src/Ghost/GRBL/SerialConnection_windows.cpp:198-231
 
  ### Internal-state latency
 
  Approximately:
 
  serial delivery + next active read
 
  Exact timing depends on firmware, OS, driver, and whether a response reader is currently active.
 
  ### UI latency
 
  The UI connection-status timer runs every 100 ms:
 
  - UI/src/Main/DDController.js:75-78
 
  The program-progress timer also runs approximately every 100 ms while active:
 
  - UI/src/Renderer/containers/Milling/Milling.js:449-499
 
  So once DDCut has stored the user-visible error, UI reflection is nominally within approximately
  100 ms plus IPC/render scheduling.
 
  ## Timeout-based e-stop
 
  Normal timeout sequence:
 
  last received input
      -> approximately 2 seconds with no input
      -> GS_TIMEOUT
      -> next ReadResponse() iteration
      -> alarm 50 / ESTOP_PUSHED
 
  The response reader sleeps 5 ms per loop:
 
  - src/Ghost/GRBL/GhostConnection.cpp:481-485
 
  During homing, the timeout threshold is approximately 25 seconds:
 
  - src/Ghost/GRBL/ConnectionState.h:90-95
 
  During manual operation, timeout is ignored rather than classified as e-stop:
 
  - src/Ghost/GRBL/GhostConnection.cpp:498-502
 
  ## Latency by state
 
   State                                    Practical latency
  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━
   Idle with active main UI status timer    Approximately 2 seconds for timeout-based detection,
                                            plus API scheduling; explicit alarm depends on serial
                                            response delivery
  ───────────────────────────────────────  ─────────────────────────────────────────────────────────
   Idle with Shuttle status view            ? roughly every 200 ms, subject to cache and serial
                                            response
  ───────────────────────────────────────  ─────────────────────────────────────────────────────────
   Idle without relevant API calls          No universal bound from the daemon itself
  ───────────────────────────────────────  ─────────────────────────────────────────────────────────
   Manual jog                               Explicit alarm can be processed during response/status
                                            activity; timeout-to-e-stop is suppressed
  ───────────────────────────────────────  ─────────────────────────────────────────────────────────
   Program running                          Explicit alarm is processed during response reads;
                                            timeout conversion is roughly 2 seconds after last
                                            input while ReadResponse() is active
  ───────────────────────────────────────  ─────────────────────────────────────────────────────────
   Program paused                           No separate e-stop cadence
  ───────────────────────────────────────  ─────────────────────────────────────────────────────────
   Homing                                   Timeout threshold is approximately 25 seconds
  ───────────────────────────────────────  ─────────────────────────────────────────────────────────
   UI display                               Usually another approximately 100 ms API/UI polling
                                            interval after internal error state is available
 
  ———
 
 # 8. Level-triggered or edge-triggered?
 
  ## Retained alarm state
 
  The alarm is stored as a retained atomic integer:
 
  - src/Ghost/GRBL/ConnectionState.h:176-190
 
  EstopEngaged() is simply:
 
  return m_pState->GetAlarm() == ALARM_CODE_ESTOP;
 
  - src/Ghost/GRBL/GhostConnection.h:117-120
 
  This is not a direct physical switch-level read. It is a software-level check of the last stored
  alarm.
 
  ## Press
 
  ### Demonstrated
 
  - ALARM:50 sets alarm 50.
  - A timeout during normal response processing sets alarm 50.
 
  ### Unknown
 
  The source does not establish whether the physical switch causes:
 
  - ALARM:50;
  - complete firmware silence;
  - another alarm;
  - a combination of these.
 
  That requires firmware/hardware evidence.
 
  ## While held
 
  The software alarm remains set until reset or connection replacement.
 
  If DDCut attempts a reset while the machine remains e-stopped and receives no valid response,
  reset can fail and the alarm remains.
 
  ## Release
 
  There is no direct e-stop release event or parser.
 
  ConnectionState::Reset() clears:
 
  - alarm;
  - error;
  - timeout;
  - command buffer;
  - status cache.
  - src/Ghost/GRBL/ConnectionState.h:36-51
 
  GhostConnection::Reset() eventually calls that reset after receiving/processes response data:
 
  - src/Ghost/GRBL/GhostConnection.cpp:133-165
 
  Therefore:
 
  physical release
      -> no direct DDCut event
      -> later successful reset/status exchange
      -> retained alarm may be cleared
 
  In non-manual error handling, MillingManager::CheckForError() attempts reset:
 
  - src/Ghost/MillingManager.cpp:125-163
 
  In manual operation, it returns before automatic reset:
 
  - src/Ghost/MillingManager.cpp:136-157
 
  Thus recovery behavior differs by mode.
 
 ## Restart or reconnect
 
  A new GhostConnection creates a new ConnectionState:
 
  - src/Ghost/GRBL/GhostConnection.cpp:66-73
 
  The new state starts cleared:
 
  - src/Ghost/GRBL/ConnectionState.h:34-51
 
  An unplug/reconnect discards the previous alarm state. Any physical e-stop state must be
  rediscovered from later firmware responses.
 
  ———
 
 # 9. Exact internal representation
 
  ## ConnectionState
 
  Relevant flags:
 
  GS_CONNECTED
  GS_STARTUP
  GS_ERROR
  GS_LOCKED
  GS_TIMEOUT
  GS_HOMING
 
  - src/Ghost/GRBL/ConnectionState.h:15-28
 
  There is no:
 
  GS_ESTOP
 
  The relevant stored values are:
 
  - m_alarm: numeric alarm;
  - m_error: numeric error;
  - m_state: atomic bitfield;
  - m_lastReadTime: atomic timestamp;
  - m_buffer: outstanding command/response tracking.
 
  ## MillingError
 
  enum Type {
      Alarm,
      Error
  };
 
  - src/Ghost/Status/MillingError.h:11-22
 
  The e-stop predicate is narrow:
 
  bool IsEStop() const noexcept {
      return type == Alarm && error_id == ALARM_CODE_ESTOP;
  }
 
  - src/Ghost/Status/MillingError.h:49-52
 
  ## GhostConnection::EstopEngaged()
 
  bool EstopEngaged() {
      return m_pState->GetAlarm() == ALARM_CODE_ESTOP;
  }
 
  - src/Ghost/GRBL/GhostConnection.h:117-120
 
  ## m_emergencyStop
 
  m_emergencyStop is an atomic boolean:
 
  - declaration: src/Ghost/GRBL/GhostConnection.h:166
 
  It is set only during the software emergency-stop operation:
 
  - src/Ghost/GRBL/GhostConnection.cpp:204-218
 
  It is not a persistent physical e-stop state.
 
  ———
 
 # 10. What happens after detection?
 
  ## Hardware alarm during program
 
  When Protocol::ProcessResponse() receives ALARM:50:
 
  1. ConnectionState::SetAlarm(50) stores the alarm.
  2. Generic error/locked flags are set.
  3. GhostException::ALARM is thrown.
 
  - src/Ghost/GRBL/Protocol/Protocol.cpp:89-109
  - src/Ghost/GRBL/ConnectionState.h:122-147
 
  The exception reaches MillingManager::RunGCode():
 
  - src/Ghost/MillingManager.cpp:78-123
 
  The milling thread exits and sets:
 
  m_inProgress = false;
 
  However, the catch path contains this condition:
 
  if (!pConnection->GetError().has_value()) {
      ...
  }
 
  Because alarm 50 is already retained, GetError() may already return an error. In that case the
  catch path does not immediately populate m_error.
 
  A later GetMillingStatus() call invokes CheckForError() and materializes the user-facing e-stop
  error:
 
  - src/Ghost/MillingManager.cpp:125-163
  - src/Ghost/MillingManager.cpp:165-194
 
  This is a demonstrated delayed-publication quirk.
 
  ## Does DDCut immediately stop transmitting?
 
  There is no explicit hardware-e-stop transmission command.
 
  The program executor stops after the exception reaches RunGCode(), but the alarm parser does not
  explicitly clear all queued commands at the moment it sees the alarm.
 
  ## Does it clear the command queue?
 
  Not immediately.
 
  ConnectionState::Reset() clears its response-tracking buffer:
 
  - src/Ghost/GRBL/ConnectionState.h:36-51
 
  But that occurs during a later reset/recovery path, not directly in the alarm parser.
 
  No source code explicitly cancels commands already accepted by firmware.
 
  ## Does DDCut issue a stop command for hardware e-stop?
 
  No.
 
  The physical e-stop path reacts to:
 
  - ALARM:50; or
  - no response/timeout.
 
  The software stop path sends Ctrl-X:
 
  - src/Ghost/GRBL/GhostConnection.cpp:204-218
 
 ## UI reaction
 
  The error is returned by the native API:
 
  - src/NodeWrapper.cpp:421-448
 
  The renderer processes it in the milling progress loop:
 
  - UI/src/Renderer/containers/Milling/Milling.js:461-474
 
  It:
 
  - sets progress to -1;
  - shows an alert;
  - sets milling false;
  - resets the selected step to the beginning unless retry is allowed.
 
  MillingError::AllowRetry() only permits retry for probe failures:
 
  - src/Ghost/Status/MillingError.h:24-32
 
  Therefore e-stop code 50 is not automatically retryable.
 
  ———
 
 # 11. Command queues and response behavior
 
  Commands are placed into DDCut's response-tracking buffer by GhostWriter::WriteLine():
 
  - src/Ghost/GRBL/GhostWriter.cpp:129-157
 
  Responses remove entries from that buffer when recognized as ok:
 
  - src/Ghost/GRBL/Protocol/Protocol.cpp:6-15
 
  An alarm line can arrive while commands are outstanding. It is parsed by the same response reader
  and throws before the outstanding queue is explicitly cleared.
 
  Program commands may be buffered depending on:
 
  - command type;
  - available firmware buffer space;
  - useBuffer;
  - whether the command is blocking.
 
  Relevant code:
 
  - src/Ghost/GRBL/GhostConnection.cpp:314-367
 
  The UI's GetMillingStatus() does not actively call CheckForError() while milling is in progress:
 
  - src/Ghost/MillingManager.cpp:165-171
 
  Therefore, while a program is running, the milling thread's response reader is the important
  detection mechanism.
 
  A long command queue may delay detection if DDCut is not currently inside a response-reading
  operation. Once it is inside ReadResponse(), it processes serial input and timeout state.
 
  ———
 
 # 12. Jogging behavior
 
  ## Ordinary jogging
 
  Jog() first queries status and settings:
 
  - src/Ghost/GRBL/Jogging/JogManager.cpp:7-19
 
  It then sends a $J=... jog command:
 
  - src/Ghost/GRBL/Jogging/JogManager.cpp:21-54
 
  The command goes through the normal GhostConnection and ReadResponse() path.
 
  ## Continuous jogging
 
  CalculateDistance() uses configured soft-limit ranges when available:
 
  - src/Ghost/GRBL/Jogging/JogManager.cpp:57-90
 
  The source contains a comment that a future implementation would need a jogging queue to clear
  commands:
 
  - src/Ghost/GRBL/Jogging/JogManager.cpp:41
 
  There is no dedicated internal active-jog state tied to e-stop.
 
  ## Jog cancellation
 
  StopJogging() sends realtime byte 0x85, then sends injected ?:
 
  - src/Ghost/GRBL/Jogging/JogManager.cpp:93-106
 
  This is jog cancellation, not emergency stop.
 
  ## E-stop while jogging
 
  ### Demonstrated
 
  - Explicit ALARM:50 uses the common alarm parser.
  - Timeout-based conversion to e-stop is suppressed while manual operation is enabled.
 
  ### Inferred
 
  An explicit firmware alarm may be discovered during the next jog/status response. A silent machine
  may not be classified as e-stop during manual operation because timeout handling returns early.
 
  ### Unknown
 
  The repository cannot establish what firmware does with:
 
  - an active $J command;
  - buffered jog motion;
  - physical e-stop during motion.
 
  ———
 
# 13. Program-execution behavior
 
  Program call chain:
 
  DDCutDaemon::StartMilling()
      -> MillingManager::MillOperationAsync()
      -> MillingManager::RunGCode()
      -> GhostConnection::ExecuteProgram()
 
  Relevant locations:
 
  - src/DDCutDaemon.cpp:836-841
  - src/Ghost/MillingManager.cpp:21-41, 78-123
  - src/Ghost/GRBL/GhostConnection.cpp:246-296
 
  When an e-stop-related exception occurs:
 
  1. The response reader throws.
  2. RunGCode() catches the exception.
  3. Program execution stops at the exception boundary.
  4. The milling thread ends.
  5. m_inProgress becomes false.
  6. Later status polling publishes the error.
 
  The UI shows:
 
  Job was aborted...
  start program from the very beginning
 
  The job is not presented as retryable.
 
  ### Resume
 
  No resume path is provided for physical e-stop code 50. The UI resets the selected step to zero
  unless the error explicitly allows retry.
 
  ———
 
 # 14. Startup with e-stop already active
 
  The connection handshake is:
 
  open serial port
      -> configure serial
      -> set internal connected flag
      -> set startup flag
      -> flush reads
      -> wait for "Grbl x.y"
      -> wait for startup flag to clear
      -> send "$$"
      -> complete connection
 
  - src/Ghost/GRBL/GhostConnection.cpp:59-122
 
  There is no:
 
  query e-stop state
  reject connection if e-stop active
 
  during connection.
 
  ### Directly demonstrated
 
  DDCut can mark the lower-level connection connected before it knows whether alarm 50 is active.
 
  ### Unknown
 
  Without firmware behavior, it cannot be determined whether an e-stopped machine:
 
  - emits the startup banner;
  - accepts $$;
  - emits ALARM:50;
  - remains silent;
  - behaves differently by firmware version.
 
  ### Inferred
 
  If the firmware completes the startup and $$ exchange while the switch is engaged, DDCut can
  report the machine connected before later status/response processing discovers e-stop.
 
  ———
 
# 15. Disconnect/reconnect effects
 
  ## USB unplug
 
  The connector checks device presence every approximately 200 ms:
 
  - src/Ghost/GhostConnector.cpp:43-63
 
  If the current path disappears:
 
  m_status = notConnected;
  m_pGhost->Disconnect();
  m_pGhost = nullptr;
 
  - src/Ghost/GhostConnector.cpp:78-96
 
  This discards the old ConnectionState and retained alarm.
 
  A new connection begins with a fresh state:
 
  - src/Ghost/GRBL/GhostConnection.cpp:66-73
  - src/Ghost/GRBL/ConnectionState.h:34-51
 
  ## Serial error
 
  A serial/protocol failure does not automatically clear the connector's m_pGhost or set connector
  status to notConnected.
 
  The connector's automatic reconnect logic is primarily driven by USB enumeration.
 
  ## Explicit software stop
 
  The software emergency-stop path does not disconnect:
 
  - src/Ghost/GRBL/GhostConnection.cpp:204-218
 
  ## Application restart
 
  All in-memory alarm/error state is discarded. There is no persisted e-stop state.
 
  ———
 
 # 16. Stock GRBL versus custom GrblDD behavior
 
  DDCut records the startup GRBL version:
 
  - src/Ghost/GRBL/Regex.h:8
  - src/Ghost/GRBL/GhostConnection.cpp:89-99
 
  The parser varies by version for:
 
  - error: handling;
  - ALARM: handling;
  - realtime status format.
  - src/Ghost/GRBL/Protocol/Protocol.cpp:25-39, 63-86, 89-109
 
  The repository's mock firmware contains Ghost Gunner metadata:
 
  [grbl:1.1h GG:3B PCB:3B VFD:3A YMD:20201212]
 
  - src/Ghost/GRBL/Mock_GRBL_Status.h:120
 
  But the production parser does not use the GG, PCB, or VFD fields for e-stop detection.
 
  No parser was found for:
 
  - Pn: e-stop pins;
  - Ghost-specific e-stop tokens;
  - GPIO;
  - USB interrupt events;
  - GG: status fields;
  - door/e-stop custom fields;
  - dedicated emergency-stop messages.
 
  ### Conclusion
 
  The source demonstrates DDCut-specific use of alarm code 50 and software code 52, but it does not
  contain the firmware implementation that emits those codes.
 
  ———
 
 # 17. False positives and conflated conditions
 
  ## Explicit alarm distinctions
 
  The error table distinguishes:
 
  - alarm 1: hard limit;
  - alarm 2: soft limit/travel;
  - alarm 3: reset while moving;
  - alarms 4-5: probe;
  - alarms 6-9: homing;
  - alarm 50: physical e-stop;
  - alarm 51: timeout;
  - alarm 52: software e-stop;
  - alarm 53: machine locked.
  - src/Ghost/Status/MillingError.cpp:53-66
 
  ## Timeout false positives
 
  The major conflation is:
 
  any normal-operation response timeout
      -> physical e-stop
 
  - src/Ghost/GRBL/GhostConnection.cpp:495-509
 
  This can classify non-e-stop failures as e-stop.
 
  ## Door
 
  The error table contains stock GRBL error 13:
 
  Check Door
 
  - src/Ghost/Status/MillingError.cpp:21-24
 
  That is an error: response mapping, not e-stop detection.
 
  There is no direct door-to-e-stop mapping.
 
  ## Limits
 
  Limit state is separately parsed as probe/X/Y/Z:
 
  - src/Ghost/GRBL/Status/LimitSwitchState.h:18-27
 
  Hard and soft limit alarms are separately mapped to alarm IDs 1 and 2.
 
  They do not become alarm 50.
 
 ## Locked/reset messages
 
  These messages:
 
  [MSG:Reset to cont]
  [MSG:Reset to continue]
 
  set GS_LOCKED and throw MACHINE_LOCKED:
 
  - src/Ghost/GRBL/Protocol/Protocol.cpp:111-115
 
  They are not physical e-stop detection.
 
  ## Feed hold
 
  Feed hold and resume use:
 
  !
  ~
 
  - UI/src/Renderer/containers/Milling/Milling.js:786-795
 
  They do not set e-stop state.
 
  ———
 
 # 18. Concrete limitations and blind spots
 
   Finding                                 Classification                          Evidence
  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━  ━━━━━━━━━━━━━━━━━━
   No dedicated e-stop pin/status          Demonstrated limitation                 RealTimeStatus.h
   parser                                                                          :58-69;
                                                                                   LimitSwitchState
                                                                                   .h:18-27
  ──────────────────────────────────────  ──────────────────────────────────────  ──────────────────
   Timeout is treated as physical e-       Demonstrated behavior and conflation    GhostConnection.
   stop                                                                            cpp:495-509
  ──────────────────────────────────────  ──────────────────────────────────────  ──────────────────
   Timeout e-stop handling is              Demonstrated limitation                 GhostConnection.
   suppressed in manual mode                                                       cpp:498-502
  ──────────────────────────────────────  ──────────────────────────────────────  ──────────────────
   No independent daemon-owned e-stop      Demonstrated limitation                 GhostConnector.c
   monitor                                                                         pp:43-63;
                                                                                   DDCutDaemon.cpp:
                                                                                   711-730
  ──────────────────────────────────────  ──────────────────────────────────────  ──────────────────
   Idle detection depends on active        Inferred risk                           DDController.js:
   API/status consumers                                                            75-78;
                                                                                   MillingManager.c
                                                                                   pp:165-171
  ──────────────────────────────────────  ──────────────────────────────────────  ──────────────────
   Alarm parsing does not immediately      Demonstrated limitation                 Protocol.cpp:89-
   clear command buffer                                                            109;
                                                                                   ConnectionState.
                                                                                   h:36-51
  ──────────────────────────────────────  ──────────────────────────────────────  ──────────────────
   Program thread stops after              Demonstrated behavior                   MillingManager.c
   exception, but firmware-buffered                                                pp:78-123;
   commands are not explicitly canceled                                            GhostWriter.cpp:
                                                                                   129-157
  ──────────────────────────────────────  ──────────────────────────────────────  ──────────────────
   User-visible e-stop error may be        Demonstrated control-flow quirk         MillingManager.c
   populated only during later status                                              pp:100-115, 125-
   polling                                                                         163
  ──────────────────────────────────────  ──────────────────────────────────────  ──────────────────
   Connection handshake does not check     Demonstrated limitation                 GhostConnection.
   e-stop state                                                                    cpp:78-122
  ──────────────────────────────────────  ──────────────────────────────────────  ──────────────────
   Physical e-stop release is not          Demonstrated limitation                 ConnectionState.
   event-driven                                                                    h:36-51;
                                                                                   GhostConnection.
                                                                                   cpp:133-165
  ──────────────────────────────────────  ──────────────────────────────────────  ──────────────────
   Physical switch behavior is absent      Unknown                                 No production
   from repository                                                                 firmware or GPIO
                                                                                   implementation
                                                                                   found
  ──────────────────────────────────────  ──────────────────────────────────────  ──────────────────
   Exact e-stop firmware message           Unknown                                 Firmware source
   differs by GG generation/version                                                absent
 
  ———
 
  # 19. Behavior/state charts
 
  ## Hardware e-stop via explicit alarm
 
  Physical e-stop pressed
      ↓
  UNKNOWN: firmware-specific hardware reaction
      ↓
  firmware may emit "ALARM:50"
      ↓
  SerialConnection::ReadLine()
      ↓
  GhostConnection::ReadResponse()
      ↓
  Protocol::ProcessResponse("ALARM:50")
      ↓
  ConnectionState::SetAlarm(50)
      ↓
  GhostException::ALARM
      ↓
  MillingManager / GetMillingStatus
      ↓
  MillingError { type=Alarm, error_id=50 }
      ↓
  MillingError::IsEStop()
  GhostConnection::EstopEngaged()
      ↓
  NodeWrapper may report connectionFailed
      ↓
  DDController forwards status
      ↓
  UI displays failure/error
 
  ## Hardware e-stop via timeout
 
  Physical e-stop or other non-response condition
      ↓
  No serial line while ReadResponse() is active
      ↓
  ConnectionState::UpdateState()
      ↓
  GS_TIMEOUT after ~2 seconds
      ↓
  Normal operation:
      SetAlarm(50)
      throw ESTOP_PUSHED
      ↓
  MillingManager exits program thread
      ↓
  Later CheckForError()/GetMillingStatus()
      ↓
  MillingError code 50
 
  Manual-operation branch:
 
 No response
      ↓
  timeout
      ↓
  manual-operation flag is true
      ↓
  log "Ignore timeout in manual operation mode"
      ↓
  return without setting alarm 50
 
  ## Software emergency stop
 
  User presses DDCut stop
      ↓
  Jobs::EmergencyStop
      ↓
  DDCutDaemon::EmergencyStop
      ↓
  GhostConnection::EmergencyStop
      ↓
  m_emergencyStop = true
      ↓
  send realtime 0x18 / Ctrl-X
      ↓
  Reset(false), sends '?'
      ↓
  ReadResponse() sees m_emergencyStop
      ↓
  throw SOFTWARE_ESTOP
      ↓
  GhostErrorHandler::GetError
      ↓
  MillingError alarm code 52
 
  ## Release/recovery
 
  Physical e-stop released
      ↓
  UNKNOWN: firmware response/clear behavior
      ↓
  Later status/command/reset receives usable response
      ↓
  GhostConnection::Reset
      ↓
  ConnectionState::Reset
      ↓
  Alarm/timeout/error state cleared
      ↓
  Machine may become usable
 
  There is no direct source-level physical-switch release event.
 
  ———
 
# 20. Direct answers to the required questions
 
  1. What exact firmware/serial indication tells DDCut hardware e-stop is active?
     ALARM:50 for the GRBL 1.1 numeric alarm path. Separately, DDCut treats a normal response
     timeout as e-stop code 50.
 
  2. Who asks the firmware for that information?
     Protocol::QueryStatus() and GhostConnection::ReadResponse() can send ?. Jogging also sends ?
     before/after jog operations.
 
  3. How often is it checked while idle?
     There is no independent daemon-owned machine-state monitor. The main UI status timer runs every
     100 ms and can indirectly perform error checking. The Shuttle status component sends ? every
     200 ms while mounted.
 
  4. How often while a program is running?
     It is coupled to command-response processing. ReadResponse() may send ? after stale input and
     converts timeout after approximately two seconds.
 
  5. Can DDCut learn about e-stop asynchronously without polling?
     POSIX serial bytes are received asynchronously, but protocol processing occurs through DDCut's
     response-reading path. There is no dedicated asynchronous e-stop event handler.
 
  6. What is the worst plausible detection delay?
     Explicit ALARM:50 depends on serial delivery and whether a response reader consumes it.
     Timeout-based detection is approximately two seconds only while ReadResponse() is active. Idle
     behavior depends on API/status consumers.
 
  7. Does DDCut have a dedicated e-stop state?
     No. It uses alarm code 50 and helper predicates.
 
  8. What changes internally?
     m_alarm becomes 50, generic error/locked flags are set, and an exception is thrown. Software
     stop additionally uses transient m_emergencyStop.
 
  9. Does program execution stop immediately?
     DDCut's program thread stops after the response exception reaches RunGCode(). Physical motion
     stopping is outside this repository.
 
  10. Are queued commands discarded?
     Not immediately by alarm parsing. A later ConnectionState::Reset() clears DDCut's response
     buffer.
 
  11. Does DDCut command the machine to stop for hardware e-stop?
     No. It reacts to an alarm or missing response. The software stop path sends Ctrl-X.
 
  12. What happens when the physical e-stop is released?
     There is no direct release detection. A later successful reset/status exchange can clear the
     retained software alarm.
 
  13. What action is needed before motion is allowed again?
     The code 50 message tells the user to release the red switch and restart from the beginning.
     Non-manual error recovery attempts reset. Exact firmware requirements are unknown.
 
  14. What happens if DDCut connects while e-stop is already active?
     There is no explicit e-stop check in the handshake. DDCut can report connected before e-stop
     state is known. Exact behavior depends on firmware.
 
  15. Can DDCut distinguish e-stop from hard limits, alarms, and other lock states?
     Explicit alarm IDs are distinguished, but timeout is conflated with e-stop. UI connection
     status projects e-stop to generic connectionFailed.
 
  16. Which behavior is stock GRBL versus custom GrblDD?
     GRBL-style ALARM: and status parsing are present. Numeric alarm 50, software alarm 52, and
     their user-facing meanings are DDCut conventions. Firmware emission of code 50 is not present
     in this repository.
 
  17. What concrete shortcomings exist?
     No pin parser, no independent e-stop monitor, timeout/e-stop conflation, manual-mode timeout
     suppression, no handshake safety check, no immediate command-buffer purge, and release
     detection dependent on later status/reset activity.
 
  ———
 
 # 21. Final summary table
 
   Question                        Current DDCut behavior                          Evidence
  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━  ━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━  ━━━━━━━━━━━━━━━━━━
   Physical signal source          ALARM:50 or inferred communication timeout;     Protocol.cpp:89-
                                   exact hardware source unknown                   109;
                                                                                   GhostConnection.
                                                                                   cpp:495-509
  ──────────────────────────────  ──────────────────────────────────────────────  ──────────────────
   Protocol representation         ALARM:50; no Pn: or dedicated e-stop pin        Protocol.cpp:89-
                                   parser                                          109;
                                                                                   RealTimeStatus.h
                                                                                   :58-69
  ──────────────────────────────  ──────────────────────────────────────────────  ──────────────────
   Detection trigger               Alarm-line parsing or active response-loop      GhostConnection.
                                   timeout                                         cpp:479-518
  ──────────────────────────────  ──────────────────────────────────────────────  ──────────────────
   Idle polling cadence            No independent daemon monitor; UI/API-          DDController.js:
                                   driven, with 100 ms connection polling and      75-78;
                                   200 ms Shuttle status polling                   Operations.js:29
                                                                                   9-316
  ──────────────────────────────  ──────────────────────────────────────────────  ──────────────────
   Program-running cadence         Coupled to command-response processing; ?       GhostConnection.
                                   may be sent after stale input; timeout after    cpp:314-367,
                                   about 2 seconds                                 479-518
  ──────────────────────────────  ──────────────────────────────────────────────  ──────────────────
   Worst-case latency              Explicit alarm: serial/consumer-dependent;      ConnectionState.
                                   timeout: ~2 seconds only during active          h:84-107
                                   ReadResponse(); idle otherwise API-dependent
  ──────────────────────────────  ──────────────────────────────────────────────  ──────────────────
   Dedicated e-stop state?         No; alarm code 50 plus helper predicates        MillingError.h:4
                                                                                   9-52;
                                                                                   GhostConnection.
                                                                                   h:120
  ──────────────────────────────  ──────────────────────────────────────────────  ──────────────────
   Latched?                        Software alarm is retained until reset or       ConnectionState.
                                   connection replacement                          h:36-51, 122-147
  ──────────────────────────────  ──────────────────────────────────────────────  ──────────────────
   Program aborted?                Program thread ends after exception; UI         MillingManager.c
                                   marks milling false and shows error             pp:78-123;
                                                                                   Milling.js:461-
                                                                                   474
  ──────────────────────────────  ──────────────────────────────────────────────  ──────────────────
   Command queue cleared?          Not immediately by alarm parsing; later         Protocol.cpp:89-
                                   reset clears DDCut's response buffer            109;
                                                                                   ConnectionState.
                                                                                   h:36-51
  ──────────────────────────────  ──────────────────────────────────────────────  ──────────────────
   UI reaction                     Physical e-stop error is exposed as code 50     NodeWrapper.cpp:
                                   and may project connection status to            97-111;
                                   connectionFailed; software stop is code 52      MillingError.cpp
                                                                                   :63-66
  ──────────────────────────────  ──────────────────────────────────────────────  ──────────────────
   Recovery action                 Release physical switch, then reset/            MillingError.cpp
                                   reconnect as applicable; code 50 requires       :63;
                                   restart from beginning                          MillingManager.c
                                                                                   pp:125-163
  ──────────────────────────────  ──────────────────────────────────────────────  ──────────────────
   Connection-time check?          No explicit e-stop check                        GhostConnection.
                                                                                   cpp:78-122
  ──────────────────────────────  ──────────────────────────────────────────────  ──────────────────
   Distinguishes limits/alarms?    Explicit alarm IDs are distinguished;           MillingError.cpp
                                   timeout is conflated with e-stop                :53-66;
                                                                                   GhostConnection.
                                                                                   cpp:495-509

