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
          go_1_20 
          glibc 
        ];
      };
    };
}
