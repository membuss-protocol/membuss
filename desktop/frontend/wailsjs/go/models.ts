export namespace main {
	
	export class DesktopConfig {
	    data_dir: string;
	    setup_complete: boolean;
	    grpc_addr: string;
	    api_addr: string;
	    gateway_addr: string;
	    keep_alive: boolean;
	    auto_start: boolean;
	    installed_version: string;
	    show_betas: boolean;
	
	    static createFrom(source: any = {}) {
	        return new DesktopConfig(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.data_dir = source["data_dir"];
	        this.setup_complete = source["setup_complete"];
	        this.grpc_addr = source["grpc_addr"];
	        this.api_addr = source["api_addr"];
	        this.gateway_addr = source["gateway_addr"];
	        this.keep_alive = source["keep_alive"];
	        this.auto_start = source["auto_start"];
	        this.installed_version = source["installed_version"];
	        this.show_betas = source["show_betas"];
	    }
	}
	export class ReleaseOption {
	    tag_name: string;
	    name: string;
	    published_at: string;
	    is_latest: boolean;
	    is_current: boolean;
	    is_prerelease: boolean;
	    type: string;
	
	    static createFrom(source: any = {}) {
	        return new ReleaseOption(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.tag_name = source["tag_name"];
	        this.name = source["name"];
	        this.published_at = source["published_at"];
	        this.is_latest = source["is_latest"];
	        this.is_current = source["is_current"];
	        this.is_prerelease = source["is_prerelease"];
	        this.type = source["type"];
	    }
	}
	export class UpdateCheckResult {
	    has_update: boolean;
	    current_version: string;
	    latest_version: string;
	    is_beta: boolean;
	    installer_url?: string;
	
	    static createFrom(source: any = {}) {
	        return new UpdateCheckResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.has_update = source["has_update"];
	        this.current_version = source["current_version"];
	        this.latest_version = source["latest_version"];
	        this.is_beta = source["is_beta"];
	        this.installer_url = source["installer_url"];
	    }
	}

}

