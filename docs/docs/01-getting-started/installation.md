---
id: installation
title: Precompiled Binaries & Installation Guide
sidebar_label: Installation & Releases
---

import DownloadSection from '@site/src/components/DownloadSection';

# Installation & Precompiled Releases

Membuss provides official precompiled binaries and installers for **macOS (Apple Silicon & Intel)**, **Windows**, and **Linux (x64 & ARM64)** available directly on [GitHub Releases](https://github.com/membuss-protocol/membuss/releases).

---

## Official Precompiled Releases

<DownloadSection />

---

## 1. macOS Installation (Apple Silicon & Intel)

### Desktop GUI App (`.dmg`)
1. Download `Membuss-v2.9.1-darwin-arm64.dmg` (for M1/M2/M3/M4/M5 Macs) or `Membuss-v2.9.1-darwin-amd64.dmg` (for Intel Macs).
2. Open the `.dmg` file and drag **Membuss** to your **Applications** folder.
3. Launch **Membuss** from Spotlight or Launchpad.

### Darwin CLI (`.tar.gz`)
```bash
# Apple Silicon (M1–M5)
curl -fsSL https://github.com/membuss-protocol/membuss/releases/download/v2.9.1/membuss-v2.9.1-darwin-arm64.tar.gz | tar -xz -C /usr/local/bin

# Intel Macs
curl -fsSL https://github.com/membuss-protocol/membuss/releases/download/v2.9.1/membuss-v2.9.1-darwin-amd64.tar.gz | tar -xz -C /usr/local/bin

# Verify installation
membuss version
```

---

## 2. Windows Installation (x64 & ARM64)

### Desktop GUI Installer (`.exe`)
1. Download `Membuss-v2.9.1-windows-amd64-installer.exe`.
2. Double-click the installer and follow the setup wizard.
3. Launch **Membuss Desktop** from your Start Menu or Desktop shortcut.

### Portable Binary (`.zip`)
1. Download `membuss-v2.9.1-windows-amd64.zip`.
2. Extract the zip file to `C:\Program Files\Membuss\` or a local directory.
3. Open PowerShell or Command Prompt and run:

```powershell
.\membuss.exe daemon start --config .\membuss.yaml
```

---

## 3. Linux Installation (x64 & ARM64)

### Standalone Desktop AppImage
```bash
# Download AppImage
wget https://github.com/membuss-protocol/membuss/releases/download/v2.9.1/Membuss-v2.9.1-linux-amd64.AppImage

# Make executable
chmod +x Membuss-v2.9.1-linux-amd64.AppImage

# Run Desktop App
./Membuss-v2.9.1-linux-amd64.AppImage
```

### Headless Server Archive (`.tar.gz`)
```bash
# Download for Linux x64
wget https://github.com/membuss-protocol/membuss/releases/download/v2.9.1/membuss-v2.9.1-linux-amd64.tar.gz

# Extract binary to /usr/local/bin
sudo tar -C /usr/local/bin -xzf membuss-v2.9.1-linux-amd64.tar.gz

# Verify installation
membuss version
```

---

## Next Steps

Now that Membuss is installed, pick your path:

- **[Desktop App Guide](./desktop-app)** — GUI Node Manager, visual DAG explorer, one-click peer management.
- **[CLI Command Reference](/docs/apis-and-interfaces/cli-reference)** — Full command-line reference for `membuss daemon`, `membuss mem`, `membuss seal`, and more.
