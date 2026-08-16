import {
  GetConfig,
  SaveConfig,
  CheckNodeStatus,
  CheckExplorer,
  VerifyInstallation,
  StartNode,
  StopNode,
  CheckForUpdate,
  UpgradeBinaries,
  GetDaemonLogs
} from '../../wailsjs/go/main/App';

export interface Toast {
  id: string;
  type: 'info' | 'success' | 'warning' | 'error';
  message: string;
  timeout?: number;
}

export interface NodeStatus {
  process_running: boolean;
  api_online: boolean;
  info?: {
    peer_id?: string;
    version?: string;
    addresses?: string[];
    num_peers?: number;
    num_blocks?: number;
    repo_size?: number;
    is_anchor?: boolean;
  };
  error?: string;
}

class AppState {
  activeTab = $state('dashboard');
  config = $state<any>(null);
  installation = $state<any>({ valid: false });
  nodeStatus = $state<NodeStatus>({ process_running: false, api_online: false });
  explorerOnline = $state(false);
  logs = $state('');
  toasts = $state<Toast[]>([]);

  // Action Loading states
  loading = $state(true);
  nodeStarting = $state(false);
  nodeStopping = $state(false);
  installing = $state(false);
  updateChecking = $state(false);
  updating = $state(false);
  updateInfo = $state<any>(null);

  // Modals
  showDownloaderModal = $state(false);
  showUpdateModal = $state(false);

  private pollTimer: any = null;
  private logTimer: any = null;

  async loadApp() {
    this.loading = true;
    try {
      this.config = await GetConfig();
      this.installation = await VerifyInstallation();
      await this.refreshNodeStatus();

      if ((this.config?.setup_complete && this.installation?.valid) || this.nodeStatus?.process_running || this.nodeStatus?.api_online) {
        this.startAdaptivePolling();
      }
    } catch (e: any) {
      this.addToast('error', 'Failed to load app configuration: ' + (e.message || e));
    } finally {
      this.loading = false;
    }
  }

  async refreshNodeStatus() {
    try {
      const status = await CheckNodeStatus();
      this.nodeStatus = status;
      if (status.process_running) {
        this.explorerOnline = await CheckExplorer();
      } else {
        this.explorerOnline = false;
      }
    } catch (e: any) {
      this.nodeStatus = {
        process_running: false,
        api_online: false,
        error: e.message || String(e)
      };
      this.explorerOnline = false;
    }
  }

  startAdaptivePolling() {
    this.stopPolling();
    // Poll node status every 3 seconds when document is visible
    this.pollTimer = setInterval(() => {
      if (document.hidden) return;
      this.refreshNodeStatus();
    }, 3000);

    // Poll logs every 2 seconds when on 'logs' tab
    this.logTimer = setInterval(() => {
      if (document.hidden || this.activeTab !== 'logs') return;
      this.fetchLogs();
    }, 2000);
  }

  stopPolling() {
    if (this.pollTimer) clearInterval(this.pollTimer);
    if (this.logTimer) clearInterval(this.logTimer);
  }

  async fetchLogs() {
    try {
      this.logs = await GetDaemonLogs();
    } catch (e) {
      // silent
    }
  }

  async startNodeAction() {
    if (this.nodeStarting) return;
    this.nodeStarting = true;
    try {
      await StartNode();
      this.addToast('success', 'Membuss node starting...');
      await this.refreshNodeStatus();
    } catch (e: any) {
      this.addToast('error', 'Failed to start node: ' + (e.message || e));
    } finally {
      this.nodeStarting = false;
    }
  }

  async stopNodeAction() {
    if (this.nodeStopping) return;
    this.nodeStopping = true;
    try {
      await StopNode();
      this.addToast('info', 'Membuss node stopped');
      await this.refreshNodeStatus();
    } catch (e: any) {
      this.addToast('error', 'Failed to stop node: ' + (e.message || e));
    } finally {
      this.nodeStopping = false;
    }
  }

  async restartNodeAction() {
    try {
      this.addToast('info', 'Restarting node daemon...');
      await StopNode();
      await new Promise(r => setTimeout(r, 600));
      await StartNode();
      await this.refreshNodeStatus();
      this.addToast('success', 'Node restarted successfully');
    } catch (e: any) {
      this.addToast('error', 'Failed to restart node: ' + (e.message || e));
    }
  }

  async checkForUpdatesAction() {
    this.updateChecking = true;
    try {
      const result = await CheckForUpdate();
      if (result && result.has_update) {
        this.updateInfo = result;
        this.showUpdateModal = true;
      } else {
        this.addToast('success', 'Membuss is up to date (' + (result?.current_version || 'latest') + ')');
      }
    } catch (e: any) {
      this.addToast('error', 'Update check failed: ' + (e.message || e));
    } finally {
      this.updateChecking = false;
    }
  }

  async upgradeBinariesAction() {
    this.updating = true;
    try {
      await UpgradeBinaries();
      this.addToast('success', 'Upgraded to ' + this.updateInfo?.latest_version);
      this.showUpdateModal = false;
      await this.loadApp();
    } catch (e: any) {
      this.addToast('error', 'Upgrade failed: ' + (e.message || e));
    } finally {
      this.updating = false;
    }
  }

  addToast(type: 'info' | 'success' | 'warning' | 'error', message: string, duration = 4000) {
    const id = Math.random().toString(36).substring(2, 9);
    const toast: Toast = { id, type, message };
    this.toasts = [...this.toasts, toast];
    setTimeout(() => {
      this.toasts = this.toasts.filter(t => t.id !== id);
    }, duration);
  }
}

export const app = new AppState();
