# Later

- use stdout/stderr not STDOUT/STDERR. No need for a test.
- automatically set new session token, 30 minutes before expiration. No need for a test.
- provide way so that user can give input to stdin after the process has launched. Use named pipes,
  so that the server can restart and re-connect to the process running via the nohup sub-command.
  Extend jsdom-test, so that restart of server and sending data to stdin gets tested. The string
  sent to the subproess should additionally be written to output.log with prefix stdin. When showing
  output.log use italic and increased font for data from stdin.
- Provide a way to send a signal to the subprocess. Show signals by human readable name (like kill
  (not 9)). Extend jsdom-test for that feature.
