import { useState } from "react";
import { Button, Notice } from "./ui";
import { useI18n } from "../i18n";
import { startAgent, type SynkError } from "../services/control";

// AgentDown aparece quando o agente não responde: a interface só mostra o
// que o agente diz, então sem ele não há o que mostrar.
export function AgentDown() {
    const { t } = useI18n();
    const [busy, setBusy] = useState(false);
    const [error, setError] = useState<SynkError | null>(null);

    const start = async () => {
        setBusy(true);
        setError(null);
        try {
            await startAgent();
        } catch (e) {
            setError(e as SynkError);
        } finally {
            setBusy(false);
        }
    };

    return (
        <>
            <Notice tone="pending">
                <strong>{t("agent.down")}</strong>
                <p>{t("agent.downHint")}</p>
                <div style={{ marginTop: 10 }}>
                    <Button size="sm" icon="play" busy={busy} onClick={start}>
                        {t("agent.start")}
                    </Button>
                </div>
            </Notice>
            <Notice error={error} />
        </>
    );
}
