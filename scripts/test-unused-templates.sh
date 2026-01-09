#!/usr/bin/env bash
# Bash Strict Mode: https://github.com/guettli/bash-strict-mode
trap 'echo -e "\n🤷 🚨 🔥 Warning: A command has failed. Exiting the script. Line was ($0:$LINENO): $(sed -n "${LINENO}p" "$0" 2>/dev/null || true) 🔥 🚨 🤷 "; exit 3' ERR
set -Eeuo pipefail

# Ensure Nix environment is active, or run this script via nix develop
if [[ -z "${IN_NIX_SHELL:-}" ]]; then
    echo "Nix environment not active. Running via 'nix develop'..."
    exec nix develop --command "$0" "$@"
fi

# Check that all HTML templates are used in Go code
unused_templates=""
for template_file in internal/server/templates/*.html; do
    template_name=$(basename "$template_file")
    # Search for the template name in Go files (server and sysmon packages)
    if ! grep -r "\"$template_name\"" internal/server/*.go internal/sysmon/*.go >/dev/null 2>&1; then
        unused_templates="$unused_templates$template_file\n"
    fi
done

if [[ -n $unused_templates ]]; then
    echo "Found unused HTML templates. All templates should be used in Go code or removed:"
    echo
    echo -e "$unused_templates"
    exit 1
fi
