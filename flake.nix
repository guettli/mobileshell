{
  description = "mobileshell development environment";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-25.11";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs = { self, nixpkgs, flake-utils }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = nixpkgs.legacyPackages.${system};
      in
      {
        devShells.default = pkgs.mkShell {
          buildInputs = with pkgs; [
            shellcheck
            markdownlint-cli
            go
            golangci-lint
          ];

          shellHook = ''
            echo "Development environment loaded"
            echo "Available tools: shellcheck $(shellcheck --version | head -n2 | tail -n1), go $(go version | cut -d' ' -f3)"
          '';
        };
      }
    );
}
