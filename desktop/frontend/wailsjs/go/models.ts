export namespace agent {
	
	export class AgentInfo {
	    name: string;
	    description: string;
	    mode: string;
	
	    static createFrom(source: any = {}) {
	        return new AgentInfo(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.description = source["description"];
	        this.mode = source["mode"];
	    }
	}
	export class Error {
	    code: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new Error(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.code = source["code"];
	        this.message = source["message"];
	    }
	}
	export class Option {
	    id: string;
	    description: string;
	
	    static createFrom(source: any = {}) {
	        return new Option(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.description = source["description"];
	    }
	}
	export class DecisionRequest {
	    id: string;
	    question: string;
	    options: Option[];
	    reason: string;
	    header?: string;
	    multiple?: boolean;
	    allow_custom?: boolean;
	    created_at?: string;
	
	    static createFrom(source: any = {}) {
	        return new DecisionRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.question = source["question"];
	        this.options = this.convertValues(source["options"], Option);
	        this.reason = source["reason"];
	        this.header = source["header"];
	        this.multiple = source["multiple"];
	        this.allow_custom = source["allow_custom"];
	        this.created_at = source["created_at"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class ContextRequest {
	    id: string;
	    kind: string;
	    target: string;
	    reason: string;
	
	    static createFrom(source: any = {}) {
	        return new ContextRequest(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.kind = source["kind"];
	        this.target = source["target"];
	        this.reason = source["reason"];
	    }
	}
	export class Artifact {
	    type: string;
	    path?: string;
	    content: string;
	
	    static createFrom(source: any = {}) {
	        return new Artifact(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.path = source["path"];
	        this.content = source["content"];
	    }
	}
	export class AgentResult {
	    worker_run_id?: string;
	    status: string;
	    content?: string;
	    summary?: string;
	    artifacts?: Artifact[];
	    patch?: string;
	    findings?: string[];
	    context_requests?: ContextRequest[];
	    decision_requests?: DecisionRequest[];
	    risks?: string[];
	    runtime?: string;
	    agent?: string;
	    model?: string;
	    error?: Error;
	
	    static createFrom(source: any = {}) {
	        return new AgentResult(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.worker_run_id = source["worker_run_id"];
	        this.status = source["status"];
	        this.content = source["content"];
	        this.summary = source["summary"];
	        this.artifacts = this.convertValues(source["artifacts"], Artifact);
	        this.patch = source["patch"];
	        this.findings = source["findings"];
	        this.context_requests = this.convertValues(source["context_requests"], ContextRequest);
	        this.decision_requests = this.convertValues(source["decision_requests"], DecisionRequest);
	        this.risks = source["risks"];
	        this.runtime = source["runtime"];
	        this.agent = source["agent"];
	        this.model = source["model"];
	        this.error = this.convertValues(source["error"], Error);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	export class CapabilityPolicy {
	    repoRead: boolean;
	    repoWrite: boolean;
	    shell: boolean;
	    web: boolean;
	    subagent: boolean;
	
	    static createFrom(source: any = {}) {
	        return new CapabilityPolicy(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.repoRead = source["repoRead"];
	        this.repoWrite = source["repoWrite"];
	        this.shell = source["shell"];
	        this.web = source["web"];
	        this.subagent = source["subagent"];
	    }
	}
	
	
	
	
	export class WorkspaceSnapshot {
	    workspace_id?: string;
	    canonical_root?: string;
	    grant_revision?: number;
	
	    static createFrom(source: any = {}) {
	        return new WorkspaceSnapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.workspace_id = source["workspace_id"];
	        this.canonical_root = source["canonical_root"];
	        this.grant_revision = source["grant_revision"];
	    }
	}
	export class Run {
	    runtime_decision?: number[];
	    runtime_context?: number[];
	    runtime_control?: string;
	    pending_decision_ids?: string[];
	    pending_context_ids?: string[];
	    pending_decision_base?: number;
	    pending_context_base?: number;
	    next_decision_alias?: number;
	    next_context_alias?: number;
	    used_context_ids?: string[];
	    worker_run_id: string;
	    conversation_key?: string;
	    runtime_session_id: string;
	    runtime_binary?: string;
	    runtime_session_dir?: string;
	    runtime_session_file?: string;
	    runtime_identity: string;
	    runtime_agent: string;
	    runtime_model?: string;
	    model_source?: string;
	    routing_revision?: string;
	    logical_role?: string;
	    agent: string;
	    runtime: string;
	    status: string;
	    objective: string;
	    // Go type: time
	    created: any;
	    // Go type: time
	    updated: any;
	    capabilities: CapabilityPolicy;
	    workspace?: WorkspaceSnapshot;
	    result: AgentResult;
	
	    static createFrom(source: any = {}) {
	        return new Run(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.runtime_decision = source["runtime_decision"];
	        this.runtime_context = source["runtime_context"];
	        this.runtime_control = source["runtime_control"];
	        this.pending_decision_ids = source["pending_decision_ids"];
	        this.pending_context_ids = source["pending_context_ids"];
	        this.pending_decision_base = source["pending_decision_base"];
	        this.pending_context_base = source["pending_context_base"];
	        this.next_decision_alias = source["next_decision_alias"];
	        this.next_context_alias = source["next_context_alias"];
	        this.used_context_ids = source["used_context_ids"];
	        this.worker_run_id = source["worker_run_id"];
	        this.conversation_key = source["conversation_key"];
	        this.runtime_session_id = source["runtime_session_id"];
	        this.runtime_binary = source["runtime_binary"];
	        this.runtime_session_dir = source["runtime_session_dir"];
	        this.runtime_session_file = source["runtime_session_file"];
	        this.runtime_identity = source["runtime_identity"];
	        this.runtime_agent = source["runtime_agent"];
	        this.runtime_model = source["runtime_model"];
	        this.model_source = source["model_source"];
	        this.routing_revision = source["routing_revision"];
	        this.logical_role = source["logical_role"];
	        this.agent = source["agent"];
	        this.runtime = source["runtime"];
	        this.status = source["status"];
	        this.objective = source["objective"];
	        this.created = this.convertValues(source["created"], null);
	        this.updated = this.convertValues(source["updated"], null);
	        this.capabilities = this.convertValues(source["capabilities"], CapabilityPolicy);
	        this.workspace = this.convertValues(source["workspace"], WorkspaceSnapshot);
	        this.result = this.convertValues(source["result"], AgentResult);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class RuntimeModel {
	    id: string;
	    provider_id: string;
	    model_id: string;
	    display_name: string;
	
	    static createFrom(source: any = {}) {
	        return new RuntimeModel(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.provider_id = source["provider_id"];
	        this.model_id = source["model_id"];
	        this.display_name = source["display_name"];
	    }
	}

}

export namespace config {
	
	export class AgentRouting {
	    exposed: boolean;
	    agent: string;
	    model: string;
	
	    static createFrom(source: any = {}) {
	        return new AgentRouting(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.exposed = source["exposed"];
	        this.agent = source["agent"];
	        this.model = source["model"];
	    }
	}
	export class Storage {
	    database: string;
	
	    static createFrom(source: any = {}) {
	        return new Storage(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.database = source["database"];
	    }
	}
	export class Runtime {
	    type: string;
	    mode: string;
	    binary: string;
	    host: string;
	    base_url: string;
	    port: number;
	    session_dir?: string;
	
	    static createFrom(source: any = {}) {
	        return new Runtime(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.type = source["type"];
	        this.mode = source["mode"];
	        this.binary = source["binary"];
	        this.host = source["host"];
	        this.base_url = source["base_url"];
	        this.port = source["port"];
	        this.session_dir = source["session_dir"];
	    }
	}
	export class General {
	    log_level: string;
	    task_timeout_seconds: number;
	    max_context_bytes: number;
	
	    static createFrom(source: any = {}) {
	        return new General(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.log_level = source["log_level"];
	        this.task_timeout_seconds = source["task_timeout_seconds"];
	        this.max_context_bytes = source["max_context_bytes"];
	    }
	}
	export class Config {
	    general: General;
	    runtime: Runtime;
	    agents: Record<string, AgentRouting>;
	    storage: Storage;
	    warnings: string[];
	
	    static createFrom(source: any = {}) {
	        return new Config(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.general = this.convertValues(source["general"], General);
	        this.runtime = this.convertValues(source["runtime"], Runtime);
	        this.agents = this.convertValues(source["agents"], AgentRouting, true);
	        this.storage = this.convertValues(source["storage"], Storage);
	        this.warnings = source["warnings"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	
	

}

export namespace desktop {
	
	export class Check {
	    name: string;
	    status: string;
	    code?: string;
	    message: string;
	
	    static createFrom(source: any = {}) {
	        return new Check(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.name = source["name"];
	        this.status = source["status"];
	        this.code = source["code"];
	        this.message = source["message"];
	    }
	}
	export class RoutingDiscovery {
	    agents: agent.AgentInfo[];
	    models: agent.RuntimeModel[];
	
	    static createFrom(source: any = {}) {
	        return new RoutingDiscovery(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.agents = this.convertValues(source["agents"], agent.AgentInfo);
	        this.models = this.convertValues(source["models"], agent.RuntimeModel);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class SkillStatus {
	    can_remove: boolean;
	    status: string;
	    version: string;
	    fingerprint: string;
	    path: string;
	    expected_version: string;
	    expected_fingerprint: string;
	
	    static createFrom(source: any = {}) {
	        return new SkillStatus(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.can_remove = source["can_remove"];
	        this.status = source["status"];
	        this.version = source["version"];
	        this.fingerprint = source["fingerprint"];
	        this.path = source["path"];
	        this.expected_version = source["expected_version"];
	        this.expected_fingerprint = source["expected_fingerprint"];
	    }
	}
	export class State {
	    directory: string;
	    binary: string;
	    config: config.Config;
	    runs: agent.Run[];
	    runtime_owned: boolean;
	    runtime: opencode.RuntimeSnapshot;
	    skill: SkillStatus;
	    workspaces: store.Workspace[];
	    role_policies: Record<string, string>;
	    pi_resume_commands: Record<string, string>;
	
	    static createFrom(source: any = {}) {
	        return new State(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.directory = source["directory"];
	        this.binary = source["binary"];
	        this.config = this.convertValues(source["config"], config.Config);
	        this.runs = this.convertValues(source["runs"], agent.Run);
	        this.runtime_owned = source["runtime_owned"];
	        this.runtime = this.convertValues(source["runtime"], opencode.RuntimeSnapshot);
	        this.skill = this.convertValues(source["skill"], SkillStatus);
	        this.workspaces = this.convertValues(source["workspaces"], store.Workspace);
	        this.role_policies = source["role_policies"];
	        this.pi_resume_commands = source["pi_resume_commands"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class WorkspaceGrant {
	    workspace: store.Workspace;
	    created: boolean;
	
	    static createFrom(source: any = {}) {
	        return new WorkspaceGrant(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.workspace = this.convertValues(source["workspace"], store.Workspace);
	        this.created = source["created"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace opencode {
	
	export class RuntimeSnapshot {
	    instance: store.RuntimeInstance;
	    ownership: string;
	    health: string;
	
	    static createFrom(source: any = {}) {
	        return new RuntimeSnapshot(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.instance = this.convertValues(source["instance"], store.RuntimeInstance);
	        this.ownership = source["ownership"];
	        this.health = source["health"];
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}

}

export namespace store {
	
	export class RuntimeInstance {
	    runtime_instance_id: string;
	    runtime_type: string;
	    mode: string;
	    base_url: string;
	    host: string;
	    port: number;
	    owner_instance_id: string;
	    owner_pid: number;
	    pid: number;
	    generation: number;
	    status: string;
	    version: string;
	    // Go type: time
	    started_at: any;
	    // Go type: time
	    updated_at: any;
	
	    static createFrom(source: any = {}) {
	        return new RuntimeInstance(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.runtime_instance_id = source["runtime_instance_id"];
	        this.runtime_type = source["runtime_type"];
	        this.mode = source["mode"];
	        this.base_url = source["base_url"];
	        this.host = source["host"];
	        this.port = source["port"];
	        this.owner_instance_id = source["owner_instance_id"];
	        this.owner_pid = source["owner_pid"];
	        this.pid = source["pid"];
	        this.generation = source["generation"];
	        this.status = source["status"];
	        this.version = source["version"];
	        this.started_at = this.convertValues(source["started_at"], null);
	        this.updated_at = this.convertValues(source["updated_at"], null);
	    }
	
		convertValues(a: any, classs: any, asMap: boolean = false): any {
		    if (!a) {
		        return a;
		    }
		    if (a.slice && a.map) {
		        return (a as any[]).map(elem => this.convertValues(elem, classs));
		    } else if ("object" === typeof a) {
		        if (asMap) {
		            for (const key of Object.keys(a)) {
		                a[key] = new classs(a[key]);
		            }
		            return a;
		        }
		        return new classs(a);
		    }
		    return a;
		}
	}
	export class Workspace {
	    id: string;
	    label: string;
	    canonical_root: string;
	    state: string;
	    revision: number;
	    source: string;
	    created_at: number;
	    updated_at: number;
	
	    static createFrom(source: any = {}) {
	        return new Workspace(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.id = source["id"];
	        this.label = source["label"];
	        this.canonical_root = source["canonical_root"];
	        this.state = source["state"];
	        this.revision = source["revision"];
	        this.source = source["source"];
	        this.created_at = source["created_at"];
	        this.updated_at = source["updated_at"];
	    }
	}

}

