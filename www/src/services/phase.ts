import type { Tone } from "../components/ui";
import type { Phase } from "./types";

export const phaseTone: Record<Phase, Tone> = {
    idle: "success",
    syncing: "progress",
    preview: "review",
    waiting: "pending",
    scanning: "idle",
    paused: "pending",
    missing: "failed",
};

// nameFor é o nome que o agente daria a uma pasta: o do diretório, em
// minúsculas, com o resto virando "-". Espelha folders.NameFor.
export function nameFor(path: string): string {
    const base = path.replace(/[\\/]+$/, "").split(/[\\/]/).pop() ?? "";
    return base
        .toLowerCase()
        .replace(/[^a-z0-9._-]/g, "-")
        .replace(/^[-.]+|[-.]+$/g, "");
}
