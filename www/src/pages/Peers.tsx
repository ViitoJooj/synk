import { useCallback, useEffect, useState } from "react";
import { Badge, Button, GlassCard, Notice } from "../components/ui";
import { useI18n } from "../i18n";
import { call, type SynkError } from "../services/control";
import type { PeerInfo } from "../services/types";

export function Peers() {
    const { t } = useI18n();
    const [peers, setPeers] = useState<PeerInfo[] | null>(null);
    const [error, setError] = useState<SynkError | null>(null);
    const [busy, setBusy] = useState<string | null>(null);

    const load = useCallback(async () => {
        try {
            setPeers(await call<PeerInfo[]>("peers"));
            setError(null);
        } catch (e) {
            setError(e as SynkError);
        }
    }, []);

    useEffect(() => {
        load();
    }, [load]);

    const connect = async (name: string) => {
        setBusy(name);
        try {
            await call("connect", { peer: name });
            await load();
        } catch (e) {
            setError(e as SynkError);
        } finally {
            setBusy(null);
        }
    };

    return (
        <>
            <div className="page-head">
                <div>
                    <h1>{t("nav.peers")}</h1>
                    <p className="dim">{t("peers.subtitle")}</p>
                </div>
                <Button size="sm" icon="refresh" onClick={load}>
                    {t("common.refresh")}
                </Button>
            </div>
            <Notice error={error} />
            <GlassCard>
                {peers?.length === 0 && <p className="empty">{t("peers.none")}</p>}
                <div className="rows">
                    {peers?.map((p) => (
                        <div className="row" key={p.name}>
                            <div className="row__main">
                                <strong>{p.name}</strong>
                                <p className="dim mono">
                                    {p.os} · {p.ip}
                                    {p.online && ` · ${p.direct ? t("peers.direct") : t("peers.relayed")}`}
                                </p>
                            </div>
                            {!p.linkable ? (
                                <Badge>{t("peers.noSynk")}</Badge>
                            ) : (
                                <Badge tone={p.online ? "success" : "idle"} dot>
                                    {p.online ? t("peers.online") : t("peers.offline")}
                                </Badge>
                            )}
                            <div className="row__actions">
                                {p.current ? (
                                    <Badge tone="review">{t("peers.current")}</Badge>
                                ) : (
                                    <Button size="sm" disabled={!p.linkable} busy={busy === p.name} onClick={() => connect(p.name)}>
                                        {t("peers.connect")}
                                    </Button>
                                )}
                            </div>
                        </div>
                    ))}
                </div>
            </GlassCard>
        </>
    );
}
