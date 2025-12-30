#!/usr/bin/env bash
# Bash Strict Mode: https://github.com/guettli/bash-strict-mode
trap 'echo -e "\n🤷 🚨 🔥 Warning: A command has failed. Exiting the script. Line was ($0:$LINENO): $(sed -n "${LINENO}p" "$0" 2>/dev/null || true) 🔥 🚨 🤷 "; exit 3' ERR
set -Eeuo pipefail

# Ensure Nix environment is active, or run this script via nix develop
if [[ -z "${IN_NIX_SHELL:-}" ]]; then
    echo "Nix environment not active. Running via 'nix develop'..."
    exec nix develop --command "$0" "$@"
fi

git ls-files '*.sh' | xargs shellcheck

git ls-files '*.md' | xargs markdownlint

go_mutex=$(git ls-files '*.go' | { xargs rg -n 'Mutex' || true; })
if [[ -n $go_mutex ]]; then
    echo "Found 'Mutex' in Go source code. Please avoid mutexes. This is stateless http. There should be a way to avoid mutexes"
    echo
    echo "$go_mutex"
    exit 1
fi

# shellcheck disable=SC2046
http_locations=$(rg -n 'https?://' $(git ls-files | grep -vP '\.md$' | grep -vP 'internal/server/static') | { grep -vP 'github.com/guettli/bash-strict-mode|http://%s|Found string|example.com' || true; })
if [[ -n $http_locations ]]; then
    echo "Found string 'https://' in code. This should be avoided. All needed files should be embeded into the binary via go:embed"
    echo
    echo "$http_locations"
    exit 1
fi

# shellcheck disable=SC2046
absolute_links=$(rg -n '(href|src)="/' $(git ls-files | grep -vP 'internal/server/static') || true)
if [[ -n $absolute_links ]]; then
    echo "Found absolute links in code/html/templates. Use relative paths instead or {{.BasePath}} so the application works when served behind a reverse proxy at a sub-path like https://myserver.example.com/mobileshell"
    echo
    echo "$absolute_links"
    exit 1
fi

# Check that all HTML templates are used in Go code
echo "Checking that all HTML templates are used..."
unused_templates=""
for template_file in internal/server/templates/*.html; do
    template_name=$(basename "$template_file")
    # Search for the template name in Go files
    if ! grep -r "\"$template_name\"" internal/server/*.go >/dev/null 2>&1; then
        unused_templates="$unused_templates$template_file\n"
    fi
done

if [[ -n $unused_templates ]]; then
    echo "Found unused HTML templates. All templates should be used in Go code or removed:"
    echo
    echo -e "$unused_templates"
    exit 1
fi

golangci-lint run ./...
