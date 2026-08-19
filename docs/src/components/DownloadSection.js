import React, { useEffect, useState } from 'react';
import Link from '@docusaurus/Link';

const RELEASES_API = 'https://api.github.com/repos/membuss-protocol/membuss/releases/latest';
const RELEASES_PAGE = 'https://github.com/membuss-protocol/membuss/releases';
const DEFAULT_VERSION = 'v2.9.1';

const HARDCODED_ASSETS = {
  version: DEFAULT_VERSION,
  windowsExe: `https://github.com/membuss-protocol/membuss/releases/download/${DEFAULT_VERSION}/Membuss-${DEFAULT_VERSION}-windows-amd64-installer.exe`,
  windowsZip: `https://github.com/membuss-protocol/membuss/releases/download/${DEFAULT_VERSION}/membuss-${DEFAULT_VERSION}-windows-amd64.zip`,
  linuxAppImage: `https://github.com/membuss-protocol/membuss/releases/download/${DEFAULT_VERSION}/Membuss-${DEFAULT_VERSION}-linux-amd64.AppImage`,
  linuxTarAmd: `https://github.com/membuss-protocol/membuss/releases/download/${DEFAULT_VERSION}/membuss-${DEFAULT_VERSION}-linux-amd64.tar.gz`,
  linuxTarArm: `https://github.com/membuss-protocol/membuss/releases/download/${DEFAULT_VERSION}/membuss-${DEFAULT_VERSION}-linux-arm64.tar.gz`,
  darwinTarArm: `https://github.com/membuss-protocol/membuss/releases/download/${DEFAULT_VERSION}/membuss-${DEFAULT_VERSION}-darwin-arm64.tar.gz`,
  darwinTarAmd: `https://github.com/membuss-protocol/membuss/releases/download/${DEFAULT_VERSION}/membuss-${DEFAULT_VERSION}-darwin-amd64.tar.gz`,
  darwinArmDmg: `https://github.com/membuss-protocol/membuss/releases/download/${DEFAULT_VERSION}/Membuss-${DEFAULT_VERSION}-darwin-arm64.dmg`,
  darwinAmdDmg: `https://github.com/membuss-protocol/membuss/releases/download/${DEFAULT_VERSION}/Membuss-${DEFAULT_VERSION}-darwin-amd64.dmg`,
};

export default function DownloadSection() {
  const [os, setOs] = useState('windows');
  const [arch, setArch] = useState('amd64');
  const [releaseInfo, setReleaseInfo] = useState(HARDCODED_ASSETS);

  useEffect(() => {
    if (typeof window !== 'undefined') {
      const ua = navigator.userAgent.toLowerCase();
      if (ua.includes('win')) {
        setOs('windows');
      } else if (ua.includes('linux')) {
        setOs('linux');
      } else if (ua.includes('mac')) {
        setOs('macos');
      }

      if (ua.includes('arm') || ua.includes('aarch64')) {
        setArch('arm64');
      } else {
        setArch('amd64');
      }
    }

    fetch(RELEASES_API)
      .then((res) => res.json())
      .then((data) => {
        if (data && data.assets && data.assets.length > 0) {
          const tag = data.tag_name || DEFAULT_VERSION;
          const assetsMap = { ...HARDCODED_ASSETS, version: tag };
          data.assets.forEach((asset) => {
            const name = asset.name;
            const url = asset.browser_download_url;
            if (name.includes('windows-amd64-installer.exe')) assetsMap.windowsExe = url;
            else if (name.includes('windows-amd64.zip')) assetsMap.windowsZip = url;
            else if (name.includes('linux-amd64.AppImage')) assetsMap.linuxAppImage = url;
            else if (name.includes('linux-amd64.tar.gz')) assetsMap.linuxTarAmd = url;
            else if (name.includes('linux-arm64.tar.gz')) assetsMap.linuxTarArm = url;
            else if (name.includes('darwin-arm64.tar.gz')) assetsMap.darwinTarArm = url;
            else if (name.includes('darwin-amd64.tar.gz')) assetsMap.darwinTarAmd = url;
            else if (name.includes('darwin-arm64.dmg')) assetsMap.darwinArmDmg = url;
            else if (name.includes('darwin-amd64.dmg')) assetsMap.darwinAmdDmg = url;
          });
          setReleaseInfo(assetsMap);
        }
      })
      .catch(() => {});
  }, []);

  const getRecommendedDownload = () => {
    if (os === 'windows') {
      return {
        label: `Membuss Desktop Installer (${releaseInfo.version})`,
        sublabel: 'Desktop GUI, Node Daemon & CLI',
        url: releaseInfo.windowsExe,
        altUrl: releaseInfo.windowsZip,
        altLabel: 'Portable CLI (.zip)',
      };
    } else if (os === 'linux') {
      if (arch === 'arm64') {
        return {
          label: `Membuss Node & CLI (${releaseInfo.version})`,
          sublabel: 'Binary archive for Linux ARM64',
          url: releaseInfo.linuxTarArm,
          altUrl: releaseInfo.linuxTarAmd,
          altLabel: 'Linux x64 (.tar.gz)',
        };
      }
      return {
        label: `Membuss Desktop AppImage (${releaseInfo.version})`,
        sublabel: 'Standalone GUI for Linux x64',
        url: releaseInfo.linuxAppImage,
        altUrl: releaseInfo.linuxTarAmd,
        altLabel: 'Headless CLI (.tar.gz)',
      };
    } else if (os === 'macos') {
      if (arch === 'arm64') {
        return {
          label: `Membuss Desktop for Apple Silicon (${releaseInfo.version})`,
          sublabel: 'Universal macOS app (M1/M2/M3/M4/M5)',
          url: releaseInfo.darwinArmDmg || releaseInfo.darwinTarArm,
          altUrl: releaseInfo.darwinTarArm,
          altLabel: 'Darwin ARM64 CLI (.tar.gz)',
        };
      }
      return {
        label: `Membuss Desktop for Intel Mac (${releaseInfo.version})`,
        sublabel: 'Standalone GUI for Intel x64 Macs',
        url: releaseInfo.darwinAmdDmg || releaseInfo.darwinTarAmd,
        altUrl: releaseInfo.darwinTarAmd,
        altLabel: 'Darwin x64 CLI (.tar.gz)',
      };
    }
    return {
      label: `Download Membuss ${releaseInfo.version}`,
      sublabel: 'Precompiled binaries for macOS, Windows & Linux',
      url: RELEASES_PAGE,
      altUrl: releaseInfo.windowsZip,
      altLabel: 'Windows Zip Archive',
    };
  };

  const rec = getRecommendedDownload();

  return (
    <div id="downloads" className="downloadContainer">
      <div className="downloadHeader">
        <div className="downloadTitleGroup">
          <h3>Official Downloads & Binaries</h3>
          <span className="downloadDetectedBadge">
            {releaseInfo.version} &middot; {os.toUpperCase()} ({arch.toUpperCase()})
          </span>
        </div>
        <a
          href={RELEASES_PAGE}
          target="_blank"
          rel="noopener noreferrer"
          className="featureCardLink"
        >
          View Releases on GitHub &rarr;
        </a>
      </div>

      <div className="downloadMainCard">
        <div className="downloadMainInfo">
          <h4>{rec.label}</h4>
          <p>{rec.sublabel}</p>
        </div>
        <div className="downloadMainBtns">
          <a className="btnDownloadPrimary" href={rec.url} download>
            Download Package
          </a>
          {rec.altUrl && (
            <a className="btnDownloadSecondary" href={rec.altUrl} download>
              {rec.altLabel}
            </a>
          )}
        </div>
      </div>

      <div className="assetTableWrap">
        <table className="assetTable">
          <thead>
            <tr>
              <th>Platform</th>
              <th>Package</th>
              <th>Type</th>
              <th></th>
            </tr>
          </thead>
          <tbody>
            <tr>
              <td className="assetTablePlatform">macOS Apple Silicon</td>
              <td><code>Membuss-{releaseInfo.version}-darwin-arm64.dmg</code></td>
              <td>Desktop App (M1–M5)</td>
              <td><a href={releaseInfo.darwinArmDmg || releaseInfo.darwinTarArm}>Download</a></td>
            </tr>
            <tr>
              <td className="assetTablePlatform">macOS Apple Silicon</td>
              <td><code>membuss-{releaseInfo.version}-darwin-arm64.tar.gz</code></td>
              <td>Darwin ARM64 CLI</td>
              <td><a href={releaseInfo.darwinTarArm}>Download</a></td>
            </tr>
            <tr>
              <td className="assetTablePlatform">macOS Intel</td>
              <td><code>Membuss-{releaseInfo.version}-darwin-amd64.dmg</code></td>
              <td>Desktop App (x64)</td>
              <td><a href={releaseInfo.darwinAmdDmg || releaseInfo.darwinTarAmd}>Download</a></td>
            </tr>
            <tr>
              <td className="assetTablePlatform">macOS Intel</td>
              <td><code>membuss-{releaseInfo.version}-darwin-amd64.tar.gz</code></td>
              <td>Darwin x64 CLI</td>
              <td><a href={releaseInfo.darwinTarAmd}>Download</a></td>
            </tr>
            <tr>
              <td className="assetTablePlatform">Windows x64</td>
              <td><code>Membuss-{releaseInfo.version}-windows-amd64-installer.exe</code></td>
              <td>Desktop GUI Installer</td>
              <td><a href={releaseInfo.windowsExe}>Download</a></td>
            </tr>
            <tr>
              <td className="assetTablePlatform">Windows x64</td>
              <td><code>membuss-{releaseInfo.version}-windows-amd64.zip</code></td>
              <td>Headless CLI Binary</td>
              <td><a href={releaseInfo.windowsZip}>Download</a></td>
            </tr>
            <tr>
              <td className="assetTablePlatform">Linux x64</td>
              <td><code>Membuss-{releaseInfo.version}-linux-amd64.AppImage</code></td>
              <td>AppImage GUI Desktop</td>
              <td><a href={releaseInfo.linuxAppImage}>Download</a></td>
            </tr>
            <tr>
              <td className="assetTablePlatform">Linux x64</td>
              <td><code>membuss-{releaseInfo.version}-linux-amd64.tar.gz</code></td>
              <td>Headless Server Binary</td>
              <td><a href={releaseInfo.linuxTarAmd}>Download</a></td>
            </tr>
            <tr>
              <td className="assetTablePlatform">Linux ARM64</td>
              <td><code>membuss-{releaseInfo.version}-linux-arm64.tar.gz</code></td>
              <td>ARM64 Server Archive</td>
              <td><a href={releaseInfo.linuxTarArm}>Download</a></td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  );
}
