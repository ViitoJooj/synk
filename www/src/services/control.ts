import { invoke } from "@tauri-apps/api/core";
import { listen, type UnlistenFn } from "@tauri-apps/api/event";
import type { AgentStatus } from "./types";

// SynkError é um erro do errorx vindo do agente: o mesmo código e a mesma
// dica que a linha de comando mostraria.
export class SynkError extends Error {
    code: string;
    kind: string;
    hint?: string;
    detail?: string;

    constructor(body: { code: string; message: string; kind: string; hint?: string; cause?: string }) {
        super(body.message);
        this.name = "SynkError";
        this.code = body.code;
        this.kind = body.kind;
        this.hint = body.hint;
        this.detail = body.cause;
    }

    // O agente não está rodando, ou não está instalado.
    get notRunning(): boolean {
        return this.code === "not-running";
    }
}

function toError(e: unknown): SynkError {
    if (e && typeof e === "object" && "code" in e && "message" in e) {
        return new SynkError(e as ConstructorParameters<typeof SynkError>[0]);
    }
    return new SynkError({ code: "snk-99", message: String(e), kind: "internal error" });
}

// call manda um verbo à API de controle do agente.
export async function call<T = unknown>(verb: string, args?: unknown): Promise<T> {
    try {
        return await invoke<T>("control", { verb, args: args ?? null });
    } catch (e) {
        throw toError(e);
    }
}

// onStatus recebe o status inteiro a cada mudança, e null quando o agente
// para de responder.
export function onStatus(fn: (st: AgentStatus | null) => void): Promise<UnlistenFn> {
    return listen<AgentStatus | null>("synk://status", (e) => fn(e.payload));
}

export async function startAgent(): Promise<void> {
    try {
        await invoke("start_agent");
    } catch (e) {
        throw toError(e);
    }
}
