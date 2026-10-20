import { useEffect, useState } from "react";
import { call, onStatus } from "../services/control";
import type { AgentStatus } from "../services/types";

// useAgent é o status do agente, ao vivo. undefined enquanto carrega, null
// quando o agente não responde.
export function useAgent(): AgentStatus | null | undefined {
    const [status, setStatus] = useState<AgentStatus | null | undefined>(undefined);

    useEffect(() => {
        let alive = true;
        call<AgentStatus>("status")
            .then((st) => alive && setStatus(st))
            .catch(() => alive && setStatus(null));
        const unlisten = onStatus((st) => alive && setStatus(st));
        return () => {
            alive = false;
            unlisten.then((fn) => fn());
        };
    }, []);

    return status;
}

// useNow re-renderiza a cada intervalo, para "há 3min" andar sozinho.
export function useNow(ms = 15000): number {
    const [now, setNow] = useState(Date.now());
    useEffect(() => {
        const id = setInterval(() => setNow(Date.now()), ms);
        return () => clearInterval(id);
    }, [ms]);
    return now;
}
