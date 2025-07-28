{
  description = "Mina performance testing environment";

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
        name = "mina-perf-testing";
        buildInputs = with pkgs; [ 
          stdenv 
          go 
          glibc 
        ];
      };
    };
}