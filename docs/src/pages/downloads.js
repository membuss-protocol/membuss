import React, { useEffect, useState } from 'react';
import Link from '@docusaurus/Link';
import Layout from '@theme/Layout';

const RELEASES_API = 'https://api.github.com/repos/nnlgsakib/membuss/releases/latest';
const RELEASES_PAGE = 'https://github.com/nnlgsakib/membuss/releases';
const DEFAULT_VERSION = 'v2.4.0';

const HARDCODED_ASSETS = {
  version: DEFAULT_VERSION,
  windowsExe: `https://github.com/nnlgsakib/membuss/releases/download/${DEFAULT_VERSION}/Membuss-${DEFAULT_VERSION}-windows-amd64-installer.exe`,
  windowsZip: `https://github.com/nnlgsakib/membuss/releases/download/${DEFAULT_VERSION}/membuss-${DEFAULT_VERSION}-windows-amd64.zip`,
  linuxAppImage: `https://github.com/nnlgsakib/membuss/releases/download/${DEFAULT_VERSION}/Membuss-${DEFAULT_VERSION}-linux-amd64.AppImage`,
  linuxTarAmd: `https://github.com/nnlgsakib/membuss/releases/download/${DEFAULT_VERSION}/membuss-${DEFAULT_VERSION}-linux-amd64.tar.gz`,
  linuxTarArm: `https://github.com/nnlgsakib/membuss/releases/download/${DEFAULT_VERSION}/membuss-${DEFAULT_VERSION}-linux-arm64.tar.gz`,
};

export default function Downloads() {
  const [os, setOs] = useState('windows');
  const [arch, setArch] = useState('amd64');
  const [releaseInfo, setReleaseInfo] = useState(HARDCODED_ASSETS);

  useEffect(() => {
    if (typeof window !== 'undefined') {
      const ua = navigator.userAgent.toLowerCase();
      if (ua.includes('win')) setOs('windows');
      else if (ua.includes('linux')) setOs('linux');
      else if (ua.includes('mac')) setOs('macos');

      if (ua.includes('arm') || ua.includes('aarch64')) setArch('arm64');
      else setArch('amd64');
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
      .catch(() => {});
  }, []);

  const getRecommended = () => {
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
    }
    return {
      label: `Download Membuss ${releaseInfo.version}`,
      sublabel: 'Precompiled binaries for Windows & Linux',
      url: RELEASES_PAGE,
      altUrl: releaseInfo.windowsZip,
      altLabel: 'Windows Zip Archive',
    };
  };

  const rec = getRecommended();

  return (
    <Layout
      title="Downloads & Binaries"
      description="Download Membuss node binaries, desktop app, and CLI tools for Windows and Linux.">
      <main className="container margin-vert--lg">
        <div className="dlPage">
          <div className="dlPageHeader">
            <div>
              <h1 className="dlPageTitle">Downloads & Binaries</h1>
              <p className="dlPageSubtitle">
                {releaseInfo.version} &middot; Detected: {os.toUpperCase()} ({arch.toUpperCase()})
              </p>
            </div>
            <a
              href={RELEASES_PAGE}
              target="_blank"
              rel="noopener noreferrer"
              className="featureCardLink"
            >
              View All Releases on GitHub &rarr;
            </a>
          </div>

          <div className="dlRecommended">
            <div className="dlRecInfo">
              <span className="dlRecBadge">Recommended for your system</span>
              <h3 className="dlRecTitle">{rec.label}</h3>
              <p className="dlRecSub">{rec.sublabel}</p>
            </div>
            <div className="dlRecBtns">
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

          <div className="dlAllAssets">
            <h2 className="dlAllAssetsTitle">All Precompiled Binaries</h2>
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

          <div className="dlNotes">
            <h3 className="dlNotesTitle">Quick Reference</h3>
            <div className="dlNotesGrid">
              <div className="dlNote">
                <h4>Desktop Installer (Windows)</h4>
                <p>Full GUI application with node daemon, file browser, peer explorer, and integrated CLI. Runs as a service.</p>
              </div>
              <div className="dlNote">
                <h4>AppImage (Linux x64)</h4>
                <p>Standalone GUI executable. No installation needed. <code>chmod +x</code> and run directly.</p>
              </div>
              <div className="dlNote">
                <h4>Headless CLI Binary</h4>
                <p>Command-line only. For servers, Docker containers, and headless deployments. Manage nodes via <code>membuss</code> CLI or gRPC.</p>
              </div>
              <div className="dlNote">
                <h4>ARM64 Archive</h4>
                <p>For Raspberry Pi 4+, AWS Graviton, and other ARM64 devices. Headless server binary only.</p>
              </div>
            </div>
          </div>
        </div>
      </main>
    </Layout>
  );
}
