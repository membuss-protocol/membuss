import React from 'react';
import clsx from 'clsx';
import Link from '@docusaurus/Link';
import useDocusaurusContext from '@docusaurus/useDocusaurusContext';
import Layout from '@theme/Layout';
import DownloadSection from '@site/src/components/DownloadSection';

function HomepageHeader() {
  const {siteConfig} = useDocusaurusContext();
  return (
    <header className="hero">
      <div className="container">
        <h1>Decentralized Storage &<br />Streaming Infrastructure</h1>
        <p className="hero__subtitle">
          Membuss pairs Reed-Solomon erasure coding with parallel BLAKE3 Merkle
          DAG construction, O(1) Counting Bloom Filters, and multiplexed libp2p
          block exchange.
        </p>
        <div className="specPills">
          <span className="specPill">Redundancy: <strong>Reed-Solomon 10+4</strong></span>
          <span className="specPill">Hash: <strong>BLAKE3</strong></span>
          <span className="specPill">Bloom: <strong>O(1) Counting Filter</strong></span>
          <span className="specPill">Store: <strong>Pebble SSTable Hybrid</strong></span>
        </div>
      </div>
    </header>
  );
}

const FeatureList = [
  {
    title: 'Precompiled Releases',
    icon: '\u26A1',
    description:
      'Official binaries for Windows (.exe installer & .zip), Linux (.AppImage GUI, .tar.gz amd64 & arm64) compiled with zero CGO dependencies.',
    link: '/docs/getting-started/installation',
  },
  {
    title: 'Erasure Coding',
    icon: '\uD83D\uDEE1\uFE0F',
    description:
      'Every stored payload is sharded into 10 data + 4 parity shards using SIMD Galois Field arithmetic. Any 4 shards can fail without content loss.',
    link: '/docs/architecture/erasure-coding',
  },
  {
    title: 'Parallel Merkle Ingestion',
    icon: '\u26A1',
    description:
      'Multi-threaded BuildParallel worker pool hashes chunks concurrently with BLAKE3 and builds Merkle trees without CPU bottlenecks.',
    link: '/docs/low-level-specs/chunking-and-hashing',
  },
  {
    title: 'Counting Bloom Filter',
    icon: '\uD83D\uDD2C',
    description:
      'Thread-safe 8-bit saturating counter filter with Kirsch-Mitzenmacher double hashing for O(1) additions, deletions, and existence checks.',
    link: '/docs/low-level-specs/counting-bloom-filter',
  },
  {
    title: 'Pebble Hybrid Store',
    icon: '\uD83D\uDCBE',
    description:
      'Pebble DB engine keeps blocks <1 MB inside LSM SSTables, offloading large blobs to flat files to eliminate inode exhaustion.',
    link: '/docs/low-level-specs/pebble-hybrid-store',
  },
  {
    title: 'Memex Streaming Exchange',
    icon: '\uD83C\uDF10',
    description:
      'Multiplexed libp2p block transfer protocol with AIMD sliding window flow control, peer latency ranking, and first-byte streaming.',
    link: '/docs/core-protocols/memex',
  },
];

function FeatureCard({icon, title, description, link}) {
  return (
    <div className={clsx('col col--4')} style={{marginBottom: '1.5rem'}}>
      <div className="featureCard">
        <div className="featureCardIcon">{icon}</div>
        <h3 className="featureCardTitle">{title}</h3>
        <p className="featureCardBody">{description}</p>
        {link && (
          <Link to={link} className="featureCardLink">
            Details <span aria-hidden="true">&rarr;</span>
          </Link>
        )}
      </div>
    </div>
  );
}

export default function Home() {
  const {siteConfig} = useDocusaurusContext();
  return (
    <Layout
      title={siteConfig.title}
      description="Decentralized, Content-Addressed Storage & Delivery Infrastructure.">
      <HomepageHeader />
      <main style={{padding: '2rem 0 5rem'}}>
        <section className="container">
          <DownloadSection />
          <div className="row" style={{marginTop: '2rem'}}>
            {FeatureList.map((props, idx) => (
              <FeatureCard key={idx} {...props} />
            ))}
          </div>
        </section>
      </main>
    </Layout>
  );
}
