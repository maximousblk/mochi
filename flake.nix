{
  description = "Mochi: native desktop app for Tailcat hosts";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  # packages.<system>.Mochi is the app of that platform, and
  # packages.<system>.MochiInstaller its installer. Both come from `mygo build`
  # (the MyGo CLI pinned in go.mod as a tool), as releases do:
  #
  #   linux    Mochi: the executable, wrapped with its runtime libraries.
  #            MochiInstaller: MyGo's tarball with install.sh, which installs
  #            for the user in ~/.local.
  #   darwin   Mochi: Mochi.app. MochiInstaller: none. A disk image needs
  #            macOS's hdiutil, so the build fails here with that reason.
  #   windows  Mochi: mochi.exe. MochiInstaller: the one-step NSIS installer
  #            of packaging/windows/installer.nsi.
  #
  # Every target is built from the host named in `host`: Go cross-compiles
  # without cgo, and NSIS runs on Linux.
  outputs = { self, nixpkgs }:
    let
      lib = nixpkgs.lib;

      version = (builtins.fromJSON (builtins.readFile ./mygo.json)).version;
      vendorHash = "sha256-4DC5NbaCIo0TCvTrOz06rfS8GiWXaZyydhaoK1YmUxk=";

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

      # `mygo build` of one target: what it writes for <goos>-<goarch>. It adds
      # what a plain go build lacks: the production flag, the app's identity
      # and, on Windows, its icon and manifest. No cgo anywhere: MyGo loads
      # the desktop libraries at run time with purego.
      mygoBuild = t:
        let pkgs = nixpkgs.legacyPackages.${t.host};
        in (pkgs.buildGoModule.override { go = pkgs.go_1_27; }) {
          pname = "mochi-${t.goos}-${t.goarch}";
          inherit version vendorHash;
          src = self;
          env.CGO_ENABLED = "0";
          # mygo build also makes MyGo's own Windows installer, with makensis.
          nativeBuildInputs = lib.optionals (t.goos == "windows") [ pkgs.nsis ];
          # The tests run local relays and take minutes; run them with go test.
          doCheck = false;
          # build-tags.txt leaves out the tailscale.com features Tailcat doesn't
          # use, as Tailcat's own releases do; mygo build passes the tags of
          # GOFLAGS on and adds its own. build_tags_test.go keeps the file in
          # step with the pinned Tailcat.
          buildPhase = ''
            runHook preBuild
            export HOME=$TMPDIR
            export GOFLAGS="$GOFLAGS -tags=$(cat build-tags.txt)"
            go tool mygo build -platform ${t.goos}/${t.goarch} -skip-dmg -o "$TMPDIR/mygo-out"
            runHook postBuild
          '';
          installPhase = ''
            runHook preInstall
            cp -r "$TMPDIR/mygo-out/${t.goos}-${t.goarch}" $out
            runHook postInstall
          '';
        };

      meta = {
        description = "Native desktop app for Tailcat hosts";
        homepage = "https://github.com/maximousblk/mochi";
        license = lib.licenses.mit;
        mainProgram = "mochi";
      };

      exeName = t: if t.goos == "windows" then "mochi.exe" else "mochi";

      # Linux: the executable, wrapped with the desktop libraries it loads.
      linuxProgram = t:
        let pkgs = nixpkgs.legacyPackages.${t.host};
        in pkgs.runCommand "mochi-${version}" {
          nativeBuildInputs = [ pkgs.makeWrapper ];
          inherit meta;
        } ''
          mkdir -p $out/bin
          makeWrapper ${mygoBuild t}/mochi $out/bin/mochi \
            --prefix LD_LIBRARY_PATH : ${lib.makeLibraryPath (linuxGraphics pkgs)}
        '';

      # Linux: MyGo's tarball of the executable, desktop entry, icon and install.sh.
      linuxArchive = t:
        let pkgs = nixpkgs.legacyPackages.${t.host};
        in pkgs.runCommand "mochi-${version}-linux-${t.goarch}.tar.gz" { } ''
          cp ${mygoBuild t}/mochi-${version}-linux-${t.goarch}.tar.gz $out
        '';

      # Mochi.app, as mygo build bundles it: Info.plist, icon and executable.
      darwinApp = t:
        let pkgs = nixpkgs.legacyPackages.${t.host};
        in pkgs.runCommand "Mochi.app" { inherit meta; } ''
          cp -r ${mygoBuild t}/Mochi.app $out
        '';

      # Windows: mochi.exe.
      windowsExe = t:
        let pkgs = nixpkgs.legacyPackages.${t.host};
        in pkgs.runCommand "mochi-${version}" { inherit meta; } ''
          mkdir -p $out/bin
          cp ${mygoBuild t}/Mochi.exe $out/bin/mochi.exe
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
          echo "Build it on a Mac: go tool mygo build -platform darwin/universal (see docs/INSTALL.md)." >&2
          exit 1
        '';

      mochi = system:
        let t = targets.${system};
        in if t.goos == "darwin" then darwinApp t
        else if t.goos == "windows" then windowsExe t
        else linuxProgram t;

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
