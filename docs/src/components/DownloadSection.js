import React, { useEffect, useState } from 'react';
import Link from '@docusaurus/Link';

const RELEASES_API = 'https://api.github.com/repos/nnlgsakib/membuss/releases/latest';
const RELEASES_PAGE = 'https://github.com/nnlgsakib/membuss/releases';
const DEFAULT_VERSION = 'v2.3.0';

const HARDCODED_ASSETS = {
  version: DEFAULT_VERSION,
  windowsExe: `https://github.com/nnlgsakib/membuss/releases/download/${DEFAULT_VERSION}/Membuss-${DEFAULT_VERSION}-windows-amd64-installer.exe`,
  windowsZip: `https://github.com/nnlgsakib/membuss/releases/download/${DEFAULT_VERSION}/membuss-${DEFAULT_VERSION}-windows-amd64.zip`,
  linuxAppImage: `https://github.com/nnlgsakib/membuss/releases/download/${DEFAULT_VERSION}/Membuss-${DEFAULT_VERSION}-linux-amd64.AppImage`,
  linuxTarAmd: `https://github.com/nnlgsakib/membuss/releases/download/${DEFAULT_VERSION}/membuss-${DEFAULT_VERSION}-linux-amd64.tar.gz`,
  linuxTarArm: `https://github.com/nnlgsakib/membuss/releases/download/${DEFAULT_VERSION}/membuss-${DEFAULT_VERSION}-linux-arm64.tar.gz`,
};

export default function DownloadSection() {
  const [os, setOs] = useState('windows');
  const [arch, setArch] = useState('amd64');
  const [releaseInfo, setReleaseInfo] = useState(HARDCODED_ASSETS);
  const [loading, setLoading] = useState(true);

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
          });
          setReleaseInfo(assetsMap);
        }
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []);

  const getRecommendedDownload = () => {
    if (os === 'windows') {
      return {
        label: `Membuss Desktop Installer (${releaseInfo.version} - Windows x64)`,
        sublabel: 'Includes GUI Node Manager, Desktop Client & CLI',
        url: releaseInfo.windowsExe,
        filename: `Membuss-${releaseInfo.version}-windows-amd64-installer.exe`,
        altUrl: releaseInfo.windowsZip,
        altLabel: 'Portable CLI (.zip)',
      };
    } else if (os === 'linux') {
      if (arch === 'arm64') {
        return {
          label: `Membuss Daemon & CLI (${releaseInfo.version} - Linux ARM64)`,
          sublabel: 'Binary archive for ARM64 servers & single-board computers',
          url: releaseInfo.linuxTarArm,
          filename: `membuss-${releaseInfo.version}-linux-arm64.tar.gz`,
          altUrl: releaseInfo.linuxTarAmd,
          altLabel: 'Linux x64 (.tar.gz)',
        };
      }
      return {
        label: `Membuss Desktop AppImage (${releaseInfo.version} - Linux x64)`,
        sublabel: 'Standalone AppImage GUI Executable for Linux Desktop',
        url: releaseInfo.linuxAppImage,
        filename: `Membuss-${releaseInfo.version}-linux-amd64.AppImage`,
        altUrl: releaseInfo.linuxTarAmd,
        altLabel: 'Headless CLI (.tar.gz)',
      };
    }
    return {
      label: `Download Membuss ${releaseInfo.version}`,
      sublabel: 'View all precompiled release assets on GitHub',
      url: RELEASES_PAGE,
      filename: `Membuss-${releaseInfo.version}`,
      altUrl: releaseInfo.windowsZip,
      altLabel: 'Windows Zip Archive',
    };
  };

  const rec = getRecommendedDownload();

  return (
    <div style={{margin: '2rem 0'}}>
      <div className="downloadBox">
        <div className="downloadBoxLabel">
          Detected: {os.toUpperCase()} ({arch.toUpperCase()})
        </div>
        <div style={{display: 'flex', justifyContent: 'space-between', alignItems: 'baseline', flexWrap: 'wrap', gap: '0.5rem'}}>
          <h2 className="downloadBoxTitle">Latest Release: {releaseInfo.version}</h2>
          <a
            href={RELEASES_PAGE}
            target="_blank"
            rel="noopener noreferrer"
            style={{fontSize: '0.8rem', fontWeight: 500, color: '#6b7280'}}
          >
            All releases on GitHub
          </a>
        </div>
        <div className="downloadActions">
          <a className="btnPrimary" href={rec.url} download>
            Download
          </a>
          {rec.altUrl && (
            <a className="btnSecondary" href={rec.altUrl} download>
              {rec.altLabel}
            </a>
          )}
        </div>
        <p className="downloadNote">{rec.sublabel}</p>
      </div>

      <h3 style={{fontSize: '1rem', fontWeight: 600, marginBottom: '0.75rem'}}>
        All Binary Assets ({releaseInfo.version})
      </h3>
      <div style={{overflowX: 'auto'}}>
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
              <td style={{fontWeight: 500}}>Windows x64</td>
              <td><code>Membuss-{releaseInfo.version}-windows-amd64-installer.exe</code></td>
              <td>Desktop GUI Installer</td>
              <td><a href={releaseInfo.windowsExe}>Download</a></td>
            </tr>
            <tr>
              <td style={{fontWeight: 500}}>Windows x64</td>
              <td><code>membuss-{releaseInfo.version}-windows-amd64.zip</code></td>
              <td>Headless Binary</td>
              <td><a href={releaseInfo.windowsZip}>Download</a></td>
            </tr>
            <tr>
              <td style={{fontWeight: 500}}>Linux x64</td>
              <td><code>Membuss-{releaseInfo.version}-linux-amd64.AppImage</code></td>
              <td>AppImage GUI</td>
              <td><a href={releaseInfo.linuxAppImage}>Download</a></td>
            </tr>
            <tr>
              <td style={{fontWeight: 500}}>Linux x64</td>
              <td><code>membuss-{releaseInfo.version}-linux-amd64.tar.gz</code></td>
              <td>Headless Binary</td>
              <td><a href={releaseInfo.linuxTarAmd}>Download</a></td>
            </tr>
            <tr>
              <td style={{fontWeight: 500}}>Linux ARM64</td>
              <td><code>membuss-{releaseInfo.version}-linux-arm64.tar.gz</code></td>
              <td>Server Archive</td>
              <td><a href={releaseInfo.linuxTarArm}>Download</a></td>
            </tr>
          </tbody>
        </table>
      </div>
    </div>
  );
}
