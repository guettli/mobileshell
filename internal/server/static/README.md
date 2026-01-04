# Static Files

This directory contains static assets used by the MobileShell web interface. All files have a documented source to maintain clarity about their origin.

## File Sources

### Downloaded from npm packages (via `scripts/build.sh`)

The following files are copied from npm dependencies defined in `package.json`:

- **bootstrap.min.css**
  - Source: `node_modules/bootstrap/dist/css/bootstrap.min.css`
  - Package: `bootstrap` (^5.3.8)
  - Purpose: CSS framework for responsive design

- **htmx.min.js**
  - Source: `node_modules/htmx.org/dist/htmx.min.js`
  - Package: `htmx.org` (^2.0.8)
  - Purpose: Dynamic HTML updates without page reloads

- **idiomorph-ext.min.js**
  - Source: `node_modules/idiomorph/dist/idiomorph-ext.min.js`
  - Package: `idiomorph` (^0.7.4)
  - Purpose: DOM morphing extension for htmx

- **xterm.min.css**
  - Source: `node_modules/xterm/css/xterm.css`
  - Package: `xterm` (^5.3.0)
  - Purpose: Terminal emulator styles

- **xterm.min.js**
  - Source: `node_modules/xterm/lib/xterm.js`
  - Package: `xterm` (^5.3.0)
  - Purpose: Terminal emulator library

- **xterm-addon-fit.min.js**
  - Source: `node_modules/xterm-addon-fit/lib/xterm-addon-fit.js`
  - Package: `xterm-addon-fit` (^0.8.0)
  - Purpose: xterm addon for fitting terminal to container

- **xterm-addon-web-links.min.js**
  - Source: `node_modules/@xterm/addon-web-links/lib/addon-web-links.js`
  - Package: `@xterm/addon-web-links` (^0.11.0)
  - Purpose: xterm addon for making URLs clickable in terminal

### Custom/Handwritten Files

- **url-links.js**
  - Source: Custom code written for this project
  - Purpose: Makes URLs in output containers clickable by converting them to HTML links
  - Added in: PR #34 (commit 1617150ad51cbd12c7b6211fb852d8d321c8030a)
  - Integrates with HTMX to handle dynamically loaded content

## Updating Dependencies

To update the downloaded files:

1. Update package versions in `package.json`
2. Run `./scripts/build.sh` (which will run `pnpm install` and copy the new files)

## Embedding

These static files are embedded into the Go binary using `go:embed` directives in the server code, so the application can be distributed as a single binary.
