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
          packages =
            with pkgs;
            [
              curl
              ffmpeg
              gnugrep
              gcc
              go
              just
              vhs
            ]
            ++ pkgs.lib.optionals pkgs.stdenv.isLinux [
              vulkan-loader # Required for Vulkan accelerated encoders on Linux
            ];

          # Make GPU drivers visible for hardware-accelerated encoding
          # Linux: NixOS mounts GPU drivers under /run/opengl-driver/lib
          # macOS: VideoToolbox uses system frameworks, no LD_LIBRARY_PATH needed
          shellHook = pkgs.lib.optionalString pkgs.stdenv.isLinux ''
            # If the opengl driver directory exists, prepend it to LD_LIBRARY_PATH
            if [ -d "/run/opengl-driver/lib" ]; then
              if [ -z "$LD_LIBRARY_PATH" ]; then
                export LD_LIBRARY_PATH="/run/opengl-driver/lib"
              else
                export LD_LIBRARY_PATH="/run/opengl-driver/lib:$LD_LIBRARY_PATH"
              fi
            fi
            # Add vulkan-loader library path for h264_vulkan encoder
            export LD_LIBRARY_PATH="${pkgs.vulkan-loader}/lib:$LD_LIBRARY_PATH"
          '';
        };
      }
    );
}
