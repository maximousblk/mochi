{
  description = "Mochi: native desktop app for Tailcat hosts";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs = { self, nixpkgs }:
    let
      lib = nixpkgs.lib;

      # Systems with a package and app. Only Linux is built and checked so far.
      packageSystems = [ "x86_64-linux" "aarch64-linux" ];
      devSystems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];

      # MyGo's native UI uses the platform's graphics stack. On Linux that is
      # GTK and its neighbours; macOS and Windows use their system APIs.
      linuxGraphics = pkgs: with pkgs; lib.optionals stdenv.isLinux [
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
        libx11
        libxcursor
        libxi
        libxrandr
        libxinerama
        libxext
        libxfixes
        libxrender
        libxcb
      ];

      mochiFor = pkgs:
        (pkgs.buildGoModule.override { go = pkgs.go_1_27; }) {
          pname = "mochi";
          version = "0.1.0";
          src = self;
          vendorHash = "sha256-AqLHr35Ur890OwgLSl/YCa/PogmvGpQoRgaZZQNrXmM=";

          env.CGO_ENABLED = "1";
          nativeBuildInputs = [ pkgs.pkg-config pkgs.makeWrapper ];
          buildInputs = linuxGraphics pkgs;

          # The tests run local relays and take minutes; run them with go test.
          doCheck = false;

          postInstall = ''
            wrapProgram $out/bin/mochi \
              --prefix LD_LIBRARY_PATH : ${lib.makeLibraryPath (linuxGraphics pkgs)}
          '';

          meta = {
            description = "Native desktop app for Tailcat hosts";
            homepage = "https://github.com/maximousblk/mochi";
            license = lib.licenses.mit;
            mainProgram = "mochi";
            platforms = lib.platforms.linux;
          };
        };
    in {
      packages = lib.genAttrs packageSystems (system:
        let mochi = mochiFor (import nixpkgs { inherit system; });
        in { default = mochi; mochi = mochi; });

      apps = lib.genAttrs packageSystems (system: {
        default = {
          type = "app";
          program = "${self.packages.${system}.default}/bin/mochi";
          meta.description = "Run Mochi";
        };
      });

      # Adds pkgs.mochi to an importing flake's nixpkgs.
      overlays.default = final: _prev: { mochi = mochiFor final; };

      devShells = lib.genAttrs devSystems (system:
        let pkgs = import nixpkgs { inherit system; };
        in {
          default = pkgs.mkShell {
            packages = with pkgs; [
              go_1_27
              git
              gnumake
              pkg-config
              golangci-lint
            ];

            buildInputs = linuxGraphics pkgs;
            LD_LIBRARY_PATH = pkgs.lib.makeLibraryPath (linuxGraphics pkgs);

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
