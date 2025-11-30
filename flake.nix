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
              ffmpeg-full
              gnugrep
              gcc
              go
              just
              vhs
            ]
            ++ pkgs.lib.optionals pkgs.stdenv.isLinux [
              vulkan-loader # Required for Vulkan accelerated encoders on Linux
              intel-media-driver # VA-API driver for Intel GPUs (iHD_drv_video.so)
              vpl-gpu-rt # oneVPL runtime for Intel GPUs (QSV backend)
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
            # Add Intel media driver, and oneVPL libraries for QSV
            export LD_LIBRARY_PATH="${pkgs.intel-media-driver}/lib:$LD_LIBRARY_PATH"
            export LD_LIBRARY_PATH="${pkgs.vpl-gpu-rt}/lib:$LD_LIBRARY_PATH"
            # oneVPL runtime search path for QSV
            export ONEVPL_SEARCH_PATH="${pkgs.vpl-gpu-rt}/lib"

            # Vulkan ICD discovery: tell vulkan-loader where to find GPU drivers
            # NixOS installs ICDs under /run/opengl-driver/share/vulkan/icd.d/
            # This enables NVIDIA Vulkan on systems with both Intel iGPU and NVIDIA dGPU
            if [ -d "/run/opengl-driver/share/vulkan/icd.d" ]; then
              export VK_DRIVER_FILES=$(find /run/opengl-driver/share/vulkan/icd.d -name '*.json' 2>/dev/null | tr '\n' ':' | sed 's/:$//')
            fi
          '';
        };
      }
    );
}
