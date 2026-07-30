import React from 'react';
import Link from '@docusaurus/Link';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import Layout from '@theme/Layout';

function HomepageHero() {
  return (
    <header className="heroBanner">
      <div className="container">
        <Link to="/docs/getting-started/introduction" className="heroBadge">
          v2.4.0 — High-Performance Storage Network &rarr;
        </Link>
        <h1 className="heroTitle">
          Content-addressed storage that stays online
        </h1>
        <p className="heroSubtitle">
          Decentralized P2P network with erasure coding, instant streaming, and zero single points of failure.
        </p>

        <div className="heroActions">
          <Link to="/docs/getting-started/introduction" className="btnPrimaryAction">
            Explore Documentation &rarr;
          </Link>
          <Link to="/downloads" className="btnSecondaryAction">
            Download Node & GUI
          </Link>
          <Link to="/docs/architecture/overview" className="btnSecondaryAction">
            System Overview
          </Link>
        </div>

        <div className="metricRibbon">
          <div className="metricItem">
            <span className="metricItemLabel">Fault Tolerance</span>
            <span className="metricItemValue">40% node failure</span>
          </div>
          <div className="metricItem">
            <span className="metricItemLabel">First Byte</span>
            <span className="metricItemValue">Sub-millisecond</span>
          </div>
          <div className="metricItem">
            <span className="metricItemLabel">Verification</span>
            <span className="metricItemValue">100% cryptographic</span>
          </div>
          <div className="metricItem">
            <span className="metricItemLabel">Topology</span>
            <span className="metricItemValue">Decentralized P2P</span>
          </div>
        </div>
      </div>
    </header>
  );
}

function QuickStartPathways() {
  return (
    <section className="container" style={{ paddingTop: '3rem' }}>
      <div style={{ textAlign: 'center', marginBottom: '2rem' }}>
        <h2 className="sectionTitle">Get Started in Minutes</h2>
        <p className="sectionSubtitle">
          Everything you need to build, deploy, or run nodes on the Membuss network.
        </p>
      </div>

      <div className="quickStartGrid">
        <div className="quickStartCard">
          <div>
            <h3 className="quickStartCardTitle">Quickstart & Installation</h3>
            <p className="quickStartCardDesc">
              Run a node locally, store your first files, and query content using CLI commands or gRPC APIs.
            </p>
          </div>
          <Link to="/docs/getting-started/quickstart" className="quickStartCardLink">
            Launch Quickstart Guide &rarr;
          </Link>
        </div>

        <div className="quickStartCard">
          <div>
            <h3 className="quickStartCardTitle">System Architecture</h3>
            <p className="quickStartCardDesc">
              How content addressing, peer-to-peer routing, and erasure coding deliver 99.999% data availability.
            </p>
          </div>
          <Link to="/docs/architecture/overview" className="quickStartCardLink">
            Explore Architecture &rarr;
          </Link>
        </div>

        <div className="quickStartCard">
          <div>
            <h3 className="quickStartCardTitle">Desktop App & Dashboard</h3>
            <p className="quickStartCardDesc">
              Manage your local storage node, track network traffic, and inspect active peers.
            </p>
          </div>
          <Link to="/docs/getting-started/desktop-app" className="quickStartCardLink">
            View Desktop App &rarr;
          </Link>
        </div>
      </div>
    </section>
  );
}

const FeatureList = [
  {
    title: 'Fault-Tolerant Storage',
    description:
      'Files are sharded across multiple nodes. Up to 40% of storage providers can go offline without data loss.',
    link: '/docs/architecture/erasure-coding',
  },
  {
    title: 'High-Throughput Ingestion',
    description:
      'Multi-threaded chunking saturates modern CPUs and disk controllers for maximum write throughput.',
    link: '/docs/low-level-specs/chunking-and-hashing',
  },
  {
    title: 'Instant Content Lookup',
    description:
      'In-memory bloom filters check file existence in O(1) without disk lookups.',
    link: '/docs/low-level-specs/counting-bloom-filter',
  },
  {
    title: 'Hybrid Storage Engine',
    description:
      'Pebble DB LSM engine manages millions of blocks. Small blocks in SSTables, large blobs offloaded to flat files.',
    link: '/docs/low-level-specs/pebble-hybrid-store',
  },
  {
    title: 'Zero-Copy Streaming',
    description:
      'Multiplexed libp2p streams with AIMD flow control enable playback starting from the first byte.',
    link: '/docs/core-protocols/memex',
  },
  {
    title: 'HTTP Gateway',
    description:
      'Serve content over standard URLs with range-request video support and built-in network explorer.',
    link: '/docs/apis-and-interfaces/gateway-memgate',
  },
];

function CoreProtocolFeatures() {
  return (
    <section className="container" style={{ paddingBottom: '3rem' }}>
      <div style={{ textAlign: 'center', marginBottom: '2rem' }}>
        <h2 className="sectionTitle">Why Membuss</h2>
        <p className="sectionSubtitle">
          Engineered for performance, reliability, and easy integration.
        </p>
      </div>

      <div className="featureGrid">
        {FeatureList.map((props, idx) => (
          <div key={idx} className="featureCard">
            <div>
              <div className="featureCardHeader">
                <h3 className="featureCardTitle">{props.title}</h3>
              </div>
              <p className="featureCardBody">{props.description}</p>
            </div>
            {props.link && (
              <Link to={props.link} className="featureCardLink">
                Learn More &rarr;
              </Link>
            )}
          </div>
        ))}
      </div>

      <Link to="/downloads" className="dlPageCta">
        View All Downloads & Binaries &rarr;
      </Link>
    </section>
  );
}

export default function Home() {
  return (
    <Layout
      title="Membuss | Decentralized Storage Network"
      description="Membuss is a decentralized storage network with erasure coding, instant streaming, and peer-to-peer content delivery.">
      <HomepageHero />
      <main>
        <QuickStartPathways />
        <CoreProtocolFeatures />
      </main>
    </Layout>
  );
}
