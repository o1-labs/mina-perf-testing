{
  description = "Mina orchestrator development environment";

  inputs = {
    nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";
  };

  outputs = { self, nixpkgs }:
    let
      system = "x86_64-linux";
      pkgs = nixpkgs.legacyPackages.${system};
    in
    {
      devShells.${system}.default = pkgs.mkShell {
        name = "orchestrator-dev";
        buildInputs = with pkgs; [
          stdenv
          # Pinned to match the toolchain CI and both Dockerfiles use. A bare
          # `go` is whatever the flake's nixpkgs happens to carry, which is how
          # this shell and the old shell.nix came to disagree.
          go_1_23
          glibc
        ];
      };
    };
}
