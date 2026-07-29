---
id: installation
title: Precompiled Binaries & Installation Guide
sidebar_label: Installation & Releases
---

import DownloadSection from '@site/src/components/DownloadSection';

# Installation & Precompiled Releases

Membuss provides official precompiled binaries and installers for **Windows**, **Linux**, and **ARM architectures** available directly on [GitHub Releases](https://github.com/nnlgsakib/membuss/releases).

---

## Official Precompiled Releases

<DownloadSection />

---

## 1. Windows Installation (x64)

### Desktop GUI Installer (`.exe`)
1. Download `Membuss-v2.3.0-windows-amd64-installer.exe`.
2. Double-click the installer and follow the setup wizard.
3. Launch **Membuss Desktop** from your Start Menu or Desktop shortcut.

### Portable Binary (`.zip`)
1. Download `membuss-v2.3.0-windows-amd64.zip`.
2. Extract the zip file to `C:\Program Files\Membuss\` or a local directory.
3. Open PowerShell or Command Prompt and run:

```powershell
.\membuss.exe daemon start --config .\membuss.yaml
```

---

## 2. Linux Installation (x64 & ARM64)

### Standalone Desktop AppImage
```bash
# Download AppImage
wget https://github.com/nnlgsakib/membuss/releases/download/v2.3.0/Membuss-v2.3.0-linux-amd64.AppImage

# Make executable
chmod +x Membuss-v2.3.0-linux-amd64.AppImage

# Run Desktop App
./Membuss-v2.3.0-linux-amd64.AppImage
```

### Headless Server Archive (`.tar.gz`)
```bash
# Download for Linux x64
wget https://github.com/nnlgsakib/membuss/releases/download/v2.3.0/membuss-v2.3.0-linux-amd64.tar.gz

# Extract binary to /usr/local/bin
sudo tar -C /usr/local/bin -xzf membuss-v2.3.0-linux-amd64.tar.gz

# Verify installation
membuss version
```

---

## 3. SHA-256 Checksum Verification

Every official release includes SHA-256 cryptographic hashes for supply-chain verification:

### Windows PowerShell Verification
```powershell
Get-FileHash .\Membuss-v2.3.0-windows-amd64-installer.exe -Algorithm SHA256
```
**Expected**: `67cc37c55028dcedad9b89fa6902757252739d888f49993f30fea077fd508083`

### Linux Verification
```bash
sha256sum membuss-v2.3.0-linux-amd64.tar.gz
```
**Expected**: `8e4e99b6492a719437680dd59d48415654ad043e37de0a38272e8b202973a91c`

---

## Next Steps

Now that Membuss is installed, pick your path:

- **[Desktop App Guide](./desktop-app)** — GUI Node Manager, visual DAG explorer, one-click peer management.
- **[CLI Command Reference](/docs/apis-and-interfaces/cli-reference)** — Full command-line reference for `membuss daemon`, `membuss mem`, `membuss seal`, and more.
