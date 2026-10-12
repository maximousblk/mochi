{
  description = "Mochi: native desktop app for Tailcat hosts";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  # packages.<system>.Mochi is the app of that platform, and
  # packages.<system>.MochiInstaller its installer:
  #
  #   linux    Mochi: the executable, wrapped with its runtime libraries.
  #            MochiInstaller: a tarball with the executable, desktop entry and icon.
  #   darwin   Mochi: Mochi.app, with the icon. MochiInstaller: none. A disk image
  #            needs macOS's hdiutil, so the build fails here with that reason.
  #   windows  Mochi: mochi.exe. MochiInstaller: the NSIS installer of
  #            packaging/windows/installer.nsi.
  #
  # Every target is built from the host named in `host`: Go cross-compiles
  # without cgo, and NSIS runs on Linux.
  outputs = { self, nixpkgs }:
    let
      lib = nixpkgs.lib;

      version = "0.1.0";
      vendorHash = "sha256-AqLHr35Ur890OwgLSl/YCa/PogmvGpQoRgaZZQNrXmM=";

      targets = {
        x86_64-linux = { goos = "linux"; goarch = "amd64"; host = "x86_64-linux"; };
        aarch64-linux = { goos = "linux"; goarch = "arm64"; host = "aarch64-linux"; };
        x86_64-darwin = { goos = "darwin"; goarch = "amd64"; host = "x86_64-linux"; };
        aarch64-darwin = { goos = "darwin"; goarch = "arm64"; host = "x86_64-linux"; };
        x86_64-windows = { goos = "windows"; goarch = "amd64"; host = "x86_64-linux"; };
        aarch64-windows = { goos = "windows"; goarch = "arm64"; host = "x86_64-linux"; };
      };

      devSystems = [ "x86_64-linux" "aarch64-linux" "x86_64-darwin" "aarch64-darwin" ];

      # MyGo's native UI uses the platform's graphics stack. On Linux that is
      # GTK and its neighbours, loaded at run time with purego; macOS and
      # Windows use their system APIs.
      linuxGraphics = pkgs: with pkgs; [
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

      # The Go program of a target. No cgo anywhere: MyGo loads the desktop
      # libraries at run time with purego, so they are needed by the wrapper.
      goProgram = t:
        let pkgs = nixpkgs.legacyPackages.${t.host};
        in (pkgs.buildGoModule.override { go = pkgs.go_1_27; }) {
          pname = "mochi";
          inherit version vendorHash;
          src = self;
          env.CGO_ENABLED = "0";
          # The Go setup hook sets GOOS and GOARCH to the host's, so the target's
          # are exported only once it has run, before the build.
          preBuild = ''
            export GOOS=${t.goos} GOARCH=${t.goarch}
          '';
          nativeBuildInputs = lib.optionals (t.goos == "linux") [ pkgs.makeWrapper ];
          # The tests run local relays and take minutes; run them with go test.
          doCheck = false;
          # Windows: a GUI executable, which opens no console window.
          ldflags = [ "-s" "-w" ] ++ lib.optional (t.goos == "windows") "-H=windowsgui";
          # Go writes a cross-compiled binary to bin/<os>_<arch>/; move it up,
          # so every target has bin/mochi (bin/mochi.exe on Windows).
          postInstall = lib.optionalString (t.goos != "linux") ''
            mv $out/bin/${t.goos}_${t.goarch}/* $out/bin/
            rmdir $out/bin/${t.goos}_${t.goarch}
          '' + lib.optionalString (t.goos == "linux") ''
            wrapProgram $out/bin/mochi \
              --prefix LD_LIBRARY_PATH : ${lib.makeLibraryPath (linuxGraphics pkgs)}
          '';
          meta = {
            description = "Native desktop app for Tailcat hosts";
            homepage = "https://github.com/maximousblk/mochi";
            license = lib.licenses.mit;
            mainProgram = "mochi";
          };
        };

      exeName = t: if t.goos == "windows" then "mochi.exe" else "mochi";

      # Mochi.app: the executable, Info.plist and the icon as .icns.
      darwinApp = t:
        let pkgs = nixpkgs.legacyPackages.${t.host};
        in pkgs.runCommand "Mochi.app" {
          nativeBuildInputs = [ pkgs.libicns ];
        } ''
          app=$out/Contents
          mkdir -p $app/MacOS $app/Resources
          cp ${goProgram t}/bin/mochi $app/MacOS/Mochi
          chmod 755 $app/MacOS/Mochi
          png2icns $app/Resources/AppIcon.icns ${self}/app/resources/icon.png
          cat > $app/Info.plist <<EOF
          <?xml version="1.0" encoding="UTF-8"?>
          <!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
          <plist version="1.0">
          <dict>
            <key>CFBundleName</key><string>Mochi</string>
            <key>CFBundleDisplayName</key><string>Mochi</string>
            <key>CFBundleExecutable</key><string>Mochi</string>
            <key>CFBundleIdentifier</key><string>dev.mochi.tailcat</string>
            <key>CFBundleIconFile</key><string>AppIcon</string>
            <key>CFBundlePackageType</key><string>APPL</string>
            <key>CFBundleShortVersionString</key><string>${version}</string>
            <key>CFBundleVersion</key><string>${version}</string>
            <key>LSMinimumSystemVersion</key><string>13.0</string>
            <key>NSHighResolutionCapable</key><true/>
          </dict>
          </plist>
          EOF
        '';

      # Linux: the archive of the executable with its desktop entry and icon.
      linuxArchive = t:
        let pkgs = nixpkgs.legacyPackages.${t.host};
        in pkgs.runCommand "mochi-${version}-linux-${t.goarch}.tar.gz" {
          nativeBuildInputs = [ pkgs.gnutar pkgs.gzip ];
        } ''
          dir=$(mktemp -d)/mochi-${version}
          mkdir -p $dir
          cp ${goProgram t}/bin/mochi $dir/mochi
          cp ${self}/app/resources/icon.png $dir/mochi.png
          cat > $dir/mochi.desktop <<EOF
          [Desktop Entry]
          Type=Application
          Name=Mochi
          Exec=mochi
          Icon=mochi
          Terminal=false
          Categories=Network;
          EOF
          tar -C $(dirname $dir) -czf $out mochi-${version}
        '';

      # Windows: mochi.exe, named as Windows needs it. The Go build names the
      # file after the package, without the extension.
      windowsExe = t:
        let pkgs = nixpkgs.legacyPackages.${t.host};
        in pkgs.runCommand "mochi-${version}" { } ''
          mkdir -p $out/bin
          cp ${goProgram t}/bin/mochi.exe $out/bin/mochi.exe
        '';

      # Windows: the NSIS installer, which installs for the current user.
      windowsInstaller = t:
        let pkgs = nixpkgs.legacyPackages.${t.host};
        in pkgs.runCommand "Mochi-Setup-${version}-${t.goarch}.exe" {
          nativeBuildInputs = [ pkgs.nsis ];
        } ''
          makensis \
            -DAPP_VERSION=${version} \
            -DAPP_EXE=${windowsExe t}/bin/mochi.exe \
            -DOUT_FILE=$out \
            ${self}/packaging/windows/installer.nsi
        '';

      # A disk image is made by hdiutil, which only macOS has.
      darwinInstaller = t:
        let pkgs = nixpkgs.legacyPackages.${t.host};
        in pkgs.runCommand "Mochi-${version}-${t.goarch}.dmg" { } ''
          echo "Mochi's macOS disk image needs hdiutil, which only macOS has." >&2
          echo "Build it on a Mac with packaging/macos/ (see docs/INSTALL.md)." >&2
          exit 1
        '';

      mochi = system:
        let t = targets.${system};
        in if t.goos == "darwin" then darwinApp t
        else if t.goos == "windows" then windowsExe t
        else goProgram t;

      installer = system:
        let t = targets.${system};
        in if t.goos == "linux" then linuxArchive t
        else if t.goos == "darwin" then darwinInstaller t
        else windowsInstaller t;

      programPath = system:
        let t = targets.${system};
        in if t.goos == "darwin" then "${mochi system}/Contents/MacOS/Mochi"
        else "${mochi system}/bin/${exeName t}";
    in {
      packages = lib.genAttrs (builtins.attrNames targets) (system: {
        Mochi = mochi system;
        MochiInstaller = installer system;
        default = mochi system;
      });

      apps = lib.genAttrs (builtins.attrNames targets) (system: {
        default = {
          type = "app";
          program = programPath system;
          meta.description = "Run Mochi";
        };
      });

      # Adds pkgs.mochi, the app of the importing system, to nixpkgs.
      overlays.default = final: _prev: {
        mochi = self.packages.${final.stdenv.hostPlatform.system}.Mochi;
      };

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
              nsis # makensis: builds the Windows installer, see packaging/windows
            ];

            buildInputs = linuxGraphics pkgs;
            LD_LIBRARY_PATH = pkgs.lib.makeLibraryPath (linuxGraphics pkgs);

            shellHook = ''
              export CGO_ENABLED=0
              export GOTOOLCHAIN=local
              echo "Mochi development shell"
              echo "Go: $(go version)"
            '';
          };
        });
    };
}
