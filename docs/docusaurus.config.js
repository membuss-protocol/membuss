// @ts-check
const {themes} = require('prism-react-renderer');

/** @type {import('@docusaurus/types').Config} */
const config = {
  title: 'Membuss Docs',
  tagline: 'Decentralized, Content-Addressed Storage & Delivery Infrastructure',
  favicon: 'img/favicon.ico',

  url: 'https://membuss.io',
  baseUrl: '/',

  organizationName: 'nnlgsakib',
  projectName: 'membuss',

  onBrokenLinks: 'warn',
  onBrokenMarkdownLinks: 'warn',

  markdown: {
    mermaid: true,
  },

  themes: ['@docusaurus/theme-mermaid'],

  i18n: {
    defaultLocale: 'en',
    locales: ['en'],
  },

  presets: [
    [
      'classic',
      /** @type {import('@docusaurus/preset-classic').Options} */
      ({
        docs: {
          sidebarPath: require.resolve('./sidebars.js'),
          editUrl: 'https://github.com/nnlgsakib/membuss/tree/main/docs/',
          showLastUpdateTime: true,
        },
        theme: {
          customCss: require.resolve('./src/css/custom.css'),
        },
      }),
    ],
  ],

  themeConfig:
    /** @type {import('@docusaurus/preset-classic').ThemeConfig} */
    ({
      colorMode: {
        defaultMode: 'dark',
        disableSwitch: false,
        respectPrefersColorScheme: true,
      },
      mermaid: {
        theme: { light: 'neutral', dark: 'dark' },
      },
      navbar: {
        title: 'Membuss Protocol',
        logo: {
          alt: 'Membuss Logo',
          src: 'img/logo.png',
          srcDark: 'img/logo.png',
          width: 32,
          height: 32,
        },
        items: [
          {
            type: 'docSidebar',
            sidebarId: 'docsSidebar',
            position: 'left',
            label: 'Documentation',
          },
          {
            to: '/docs/getting-started/installation',
            position: 'left',
            label: 'Downloads & Releases',
          },
          {
            href: 'https://github.com/nnlgsakib/membuss',
            label: 'GitHub',
            position: 'right',
          },
        ],
      },
      footer: {
        style: 'dark',
        links: [
          {
            title: 'Getting Started',
            items: [
              {
                label: 'Downloads & Releases',
                to: '/docs/getting-started/installation',
              },
              {
                label: 'System Overview',
                to: '/docs/architecture/overview',
              },
              {
                label: 'Desktop GUI App',
                to: '/docs/getting-started/desktop-app',
              },
            ],
          },
          {
            title: 'Protocols & Engine',
            items: [
              {
                label: 'Pebble Hybrid Store',
                to: '/docs/low-level-specs/pebble-hybrid-store',
              },
              {
                label: 'Counting Bloom Filter',
                to: '/docs/low-level-specs/counting-bloom-filter',
              },
              {
                label: 'Memex v2 Protocol',
                to: '/docs/core-protocols/memex',
              },
            ],
          },
          {
            title: 'Community & Code',
            items: [
              {
                label: 'GitHub Repository',
                href: 'https://github.com/nnlgsakib/membuss',
              },
              {
                label: 'GitHub Releases',
                href: 'https://github.com/nnlgsakib/membuss/releases',
              },
            ],
          },
        ],
        copyright: `Copyright © ${new Date().getFullYear()} Membuss Network. Protocol Specification & Documentation.`,
      },
      prism: {
        theme: themes.github,
        darkTheme: themes.dracula,
        additionalLanguages: ['go', 'protobuf', 'bash', 'yaml', 'json'],
      },
    }),
};

module.exports = config;
