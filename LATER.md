# Later

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
