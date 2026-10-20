import { Badge, Button, GlassCard } from "../components/ui";
import type { Page } from "../components/Sidebar/Sidebar";
import { useI18n } from "../i18n";
import { useNow } from "../hooks/useAgent";
import { since } from "../services/format";
import { phaseTone } from "../services/phase";
import type { AgentStatus } from "../services/types";

export function Overview({ status, onPage }: { status: AgentStatus | null | undefined; onPage: (p: Page) => void }) {
    const { t } = useI18n();
    const now = useNow();
    if (!status) return null;

    const link = status.link;
    const up = link.state === "connected";
    const folders = status.sync?.folders ?? [];

    return (
        <>
            <div className="page-head">
                <div>
                    <h1>{t("nav.overview")}</h1>
                    <p className="dim">{t(`role.${status.role}`)}</p>
                </div>
            </div>

            <div className="grid">
                <GlassCard>
                    <h2>{t("link.title")}</h2>
                    <Badge tone={up ? "success" : link.state === "waiting" ? "pending" : "idle"} dot>
                        {t(`link.${link.state}`)}
                    </Badge>
                    <dl className="kv" style={{ marginTop: 16 }}>
                        <dt>{t("link.peer")}</dt>
                        <dd>{link.peer || t("link.anyone")}</dd>
                        {up && (
                            <>
                                <dt>{t("link.for")}</dt>
                                <dd>{since(link.since, now)}</dd>
                                <dt>{t("link.keyboard")}</dt>
                                <dd>{t(`link.focus.${link.focus ?? "peer"}`)}</dd>
                                <dt>{t("link.audio")}</dt>
                                <dd>{t(`link.audioState.${link.audio}`)}</dd>
                                <dt>{t("link.clipboard")}</dt>
                                <dd>{link.clipboard ? t("common.on") : t("common.off")}</dd>
                            </>
                        )}
                        {!up && link.last_error && (
                            <>
                                <dt>{t("link.lastDrop")}</dt>
                                <dd className="dim">{link.last_error}</dd>
                            </>
                        )}
                    </dl>
                    <div style={{ marginTop: 16 }}>
                        <Button size="sm" icon="monitor" onClick={() => onPage("peers")}>
                            {t("link.change")}
                        </Button>
                    </div>
                </GlassCard>

                <GlassCard>
                    <h2>{t("nav.folders")}</h2>
                    {folders.length === 0 ? (
                        <p className="dim">{t("folders.none")}</p>
                    ) : (
                        <div className="rows">
                            {folders.map((f) => (
                                <div className="row" key={f.name}>
                                    <div className="row__main">
                                        <strong>{f.name}</strong>
                                        <p className="dim">{f.path}</p>
                                    </div>
                                    <Badge tone={phaseTone[f.phase]}>{t(`phase.${f.phase}`)}</Badge>
                                </div>
                            ))}
                        </div>
                    )}
                    {(status.sync?.offered?.length ?? 0) > 0 && (
                        <p className="dim" style={{ marginTop: 12 }}>
                            {t("folders.offeredShort", { n: status.sync!.offered!.length })}
                        </p>
                    )}
                    <div style={{ marginTop: 16 }}>
                        <Button size="sm" icon="folder" onClick={() => onPage("folders")}>
                            {t("folders.manage")}
                        </Button>
                    </div>
                </GlassCard>
            </div>
        </>
    );
}
