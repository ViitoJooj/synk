// Espelham o JSON do agente (internal/agent, internal/sync/engine).

export interface Link {
    state: "starting" | "waiting" | "connected";
    since: string;
    peer?: string;
    peer_ip?: string;
    focus?: "local" | "peer";
    audio: "off" | "waiting" | "flowing";
    clipboard: boolean;
    last_error?: string;
}

export interface Preview {
    fetch: number;
    fetch_bytes: number;
    send: number;
    send_bytes: number;
    conflicts?: string[];
    divergent?: string[];
}

export type Phase = "waiting" | "preview" | "syncing" | "idle" | "paused" | "missing" | "scanning";

export interface FolderStatus {
    name: string;
    path: string;
    phase: Phase;
    reason?: string;
    accepted: boolean;
    files: number;
    bytes: number;
    pending: number;
    last_sync?: string;
    preview?: Preview;
    held?: string[];
    skipped?: { path: string; why: string }[];
    divergent?: string[];
}

export interface SyncStatus {
    peer?: string;
    connected: boolean;
    folders: FolderStatus[] | null;
    offered?: string[];
}

export interface AgentStatus {
    pid: number;
    started: string;
    role: "listener" | "dialer";
    link: Link;
    sync?: SyncStatus;
}

export interface PeerInfo {
    name: string;
    os: string;
    ip: string;
    online: boolean;
    direct: boolean;
    linkable: boolean;
    current: boolean;
}

export interface Setting {
    key: string;
    value: string;
    help: string;
}

export interface BlockInfo {
    what: string;
    kind: "pattern" | "path";
    origin: "yours" | "default" | "built-in";
}

export interface TrashItem {
    id: string;
    folder: string;
    path: string;
    size: number;
    trashed: string;
}
