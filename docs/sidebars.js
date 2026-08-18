// @ts-check

/** @type {import('@docusaurus/plugin-content-docs').SidebarsConfig} */
const sidebars = {
  docsSidebar: [
    {
      type: 'category',
      label: '🚀 Getting Started',
      collapsed: false,
      items: [
        'getting-started/introduction',
        'getting-started/installation',
        'getting-started/quickstart',
        'getting-started/desktop-app',
        'getting-started/configuration',
        'getting-started/deployment',
      ],
    },
    {
      type: 'category',
      label: '🏗️ System Architecture',
      collapsed: false,
      items: [
        'architecture/overview',
        'architecture/merkle-dag-memfs',
        'architecture/erasure-coding',
        'architecture/storage-engine',
      ],
    },
    {
      type: 'category',
      label: '⚡ Core Protocols',
      collapsed: false,
      items: [
        'core-protocols/memex',
        'core-protocols/mem-dht',
        'core-protocols/mem-pex',
        'core-protocols/mem-herald',
        'core-protocols/memns',
        'core-protocols/memedge',
      ],
    },
    {
      type: 'category',
      label: '🔬 Low-Level Engine Specifications',
      collapsed: false,
      items: [
        'low-level-specs/content-identifiers-mid',
        'low-level-specs/chunking-and-hashing',
        'low-level-specs/counting-bloom-filter',
        'low-level-specs/pebble-hybrid-store',
        'low-level-specs/sealing-and-gc',
      ],
    },
    {
      type: 'category',
      label: '🔌 APIs & Interfaces',
      collapsed: false,
      items: [
        'apis-and-interfaces/cli-reference',
        'apis-and-interfaces/grpc-api',
        'apis-and-interfaces/node-control-api',
        'apis-and-interfaces/gateway-memgate',
        'apis-and-interfaces/web-explorer',
      ],
    },
    {
      type: 'category',
      label: '🛡️ Operations & Extensions',
      collapsed: false,
      items: [
        'operations-and-plugins/anchor-nodes',
        'operations-and-plugins/relay-nodes',
        'operations-and-plugins/plugin-system',
        'operations-and-plugins/observability-and-metrics',
      ],
    },
  ],
};

module.exports = sidebars;
