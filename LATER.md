# Later

- File Explorer. Show all files.
- provide a way to send a signal here, too: <http://localhost:22123/workspaces/fooob/processes/f9134b00eba33574>
- show all streams in process detail view.
- double check every time.Sleep
- prepareProcessOutput: show all streams in html. Even unknown.
- Fix, OUTPUT_LOG_FORMAT.md does no longer exist.
- Add output type detection again.

---

- add-password should not be run as root user. Refuse
- add-password: if stateDir does not exist. then do not create it
- if statedir empty, and .mobileshell does not exist, try /var/lib/mobileshell-$USER.
- <http://localhost:22123/workspaces/fooob/processes/d07a0674d285a31d> link to Workspaces
  (top MobileShell) does not work. Unify that, so that all pages use the same code.
- fix flaky test.
- When the server is no longer running, and you execute a command, then there should be an error
  message, that the server could not be reached.
- Pre-command: multiline. Defaults to #!/usr/bin/env bash
- configure workspace. New page. There you can change pre-command and working-directory and name,
  but not ID
- shift-enter in "command" input will make it a textarea. ctrl-enter will send the command.
- test.sh: Execute things in parallel.
- Lint Go HTML templates?
- Check if there is a broken link via jsdom test. Or other?
- Ensure the id attribute is unique on all pages.
- autoswitch between dark and light mode.
- "MobileShell" at the top of the page should be a link to /, remove "Change Workspace" link at the
  top.
- os.FindProcess() and process.Signal(syscall.Signal(0)) gets called too often (I think)
- idiomorph. Needed?
- find duplicated code or html in templates. With a tool?
