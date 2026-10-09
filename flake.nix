{
  description = "Mochi native Tailcat desktop app";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];
      forEachSystem = f: nixpkgs.lib.genAttrs systems (system: f (import nixpkgs { inherit system; }));
    in {
      devShells = forEachSystem (pkgs:
        let
          linuxGraphics = with pkgs; lib.optionals stdenv.isLinux [
            gtk3
            glib
            pango
            cairo
            gdk-pixbuf
            atk
            at-spi2-core
            libayatana-appindicator
            libGL
            libxkbcommon
            wayland
            xorg.libX11
            xorg.libXcursor
            xorg.libXi
            xorg.libXrandr
            xorg.libXinerama
            xorg.libXext
            xorg.libXfixes
            xorg.libXrender
            xorg.libxcb
          ];
        in {
        default = pkgs.mkShell {
          packages = with pkgs; [
            go_1_27
            git
            gnumake
            pkg-config
            golangci-lint
          ];

          # MyGo native UI uses the platform's graphics stack. These Linux
          # libraries are available to development builds; macOS and Windows
          # use their system frameworks/APIs.
          buildInputs = linuxGraphics;
          LD_LIBRARY_PATH = pkgs.lib.makeLibraryPath linuxGraphics;

          shellHook = ''
            export CGO_ENABLED=1
            export GOTOOLCHAIN=local
            echo "Mochi development shell"
            echo "Go: $(go version)"
          '';
        };
      });
    };
}
