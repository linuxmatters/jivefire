{
  description = "Visualiser for linuxmatters.sh";

  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixos-unstable";
    flake-utils.url = "github:numtide/flake-utils";
  };

  outputs =
    {
      self,
      nixpkgs,
      flake-utils,
    }:

    flake-utils.lib.eachDefaultSystem (
      system:
      let
        pkgs = import nixpkgs { inherit system; };
      in
      {
        devShells.default = pkgs.mkShell {
          packages = with pkgs; [
            curl
            ffmpeg
            gnugrep
            gcc
            go
            just
            mediainfo
            vhs
          ];

          # Make GPU drivers visible for hardware-accelerated encoding (NVENC, etc.)
          # On NixOS, GPU drivers are mounted under /run/opengl-driver/lib
          shellHook = ''
            # If the opengl driver directory exists, prepend it to LD_LIBRARY_PATH
            if [ -d "/run/opengl-driver/lib" ]; then
              if [ -z "$LD_LIBRARY_PATH" ]; then
                export LD_LIBRARY_PATH="/run/opengl-driver/lib"
              else
                export LD_LIBRARY_PATH="/run/opengl-driver/lib:$LD_LIBRARY_PATH"
              fi
            fi
          '';
        };
      }
    );
}
