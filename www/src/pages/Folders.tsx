import { useState } from "react";
import { open } from "@tauri-apps/plugin-dialog";
import { Badge, Button, GlassCard, Input, Modal, Notice, Select } from "../components/ui";
import { useI18n } from "../i18n";
import { useNow } from "../hooks/useAgent";
import { call, type SynkError } from "../services/control";
import { since, size } from "../services/format";
import { nameFor, phaseTone } from "../services/phase";
import type { AgentStatus, FolderStatus } from "../services/types";
import { Blocks } from "./Blocks";
import { Trash } from "./Trash";

type Tab = "folders" | "blocks" | "trash";

export function Folders({ status }: { status: AgentStatus | null | undefined }) {
    const { t } = useI18n();
    const [tab, setTab] = useState<Tab>("folders");

    return (
        <>
            <div className="page-head">
                <div>
                    <h1>{t("nav.folders")}</h1>
                    <p className="dim">{t("folders.subtitle")}</p>
                </div>
            </div>
            <div className="tabs">
                {(["folders", "blocks", "trash"] as Tab[]).map((x) => (
                    <button key={x} className={tab === x ? "on" : ""} onClick={() => setTab(x)}>
                        {t(`folders.tab.${x}`)}
                    </button>
                ))}
            </div>
            {tab === "folders" && <FolderList status={status} />}
            {tab === "blocks" && <Blocks />}
            {tab === "trash" && <Trash />}
        </>
    );
}

function FolderList({ status }: { status: AgentStatus | null | undefined }) {
    const { t } = useI18n();
    const now = useNow();
    const [error, setError] = useState<SynkError | null>(null);
    const [adding, setAdding] = useState<{ path: string; name: string } | null>(null);
    const [reviewing, setReviewing] = useState<FolderStatus | null>(null);
    const [removing, setRemoving] = useState<FolderStatus | null>(null);
    const [deletesFor, setDeletesFor] = useState<FolderStatus | null>(null);

    const sync = status?.sync;
    const folders = sync?.folders ?? [];

    const act = async (verb: string, args: unknown) => {
        setError(null);
        try {
            await call(verb, args);
            return true;
        } catch (e) {
            setError(e as SynkError);
            return false;
        }
    };

    const pick = async (name = "") => {
        const path = await open({ directory: true, multiple: false });
        if (typeof path === "string") setAdding({ path, name: name || nameFor(path) });
    };

    return (
        <>
            <Notice error={error} />

            {sync && !sync.connected && <Notice tone="pending">{t("folders.peerAway")}</Notice>}

            {sync?.offered?.map((name) => (
                <Notice tone="progress" key={name}>
                    <strong>{t("folders.offered", { name })}</strong>
                    <p>{t("folders.offeredHint")}</p>
                    <div style={{ marginTop: 8 }}>
                        <Button size="sm" icon="plus" onClick={() => pick(name)}>
                            {t("folders.pickFor", { name })}
                        </Button>
                    </div>
                </Notice>
            ))}

            <GlassCard>
                <div className="page-head" style={{ marginBottom: 4 }}>
                    <h2 style={{ margin: 0 }}>{t("folders.synced")}</h2>
                    <Button size="sm" variant="primary" icon="plus" onClick={() => pick()}>
                        {t("folders.add")}
                    </Button>
                </div>
                {folders.length === 0 && <p className="empty">{t("folders.none")}</p>}
                <div className="rows">
                    {folders.map((f) => (
                        <div className="row" key={f.name}>
                            <div className="row__main">
                                <strong>{f.name}</strong> <span className="dim">· {t(f.files === 1 ? "folders.oneFile" : "folders.nFiles", { n: f.files })} · {size(f.bytes)}</span>
                                <p className="dim">{f.path}</p>
                                <p className="faint">{detail(f, now, t)}</p>
                            </div>
                            <Badge tone={phaseTone[f.phase]} dot={f.phase === "syncing"}>
                                {t(`phase.${f.phase}`)}
                            </Badge>
                            <div className="row__actions">
                                {f.phase === "preview" && (
                                    <Button size="sm" variant="primary" onClick={() => setReviewing(f)}>
                                        {t("folders.review")}
                                    </Button>
                                )}
                                {f.phase === "paused" && f.reason?.includes("accept-deletes") ? (
                                    <Button size="sm" onClick={() => setDeletesFor(f)}>
                                        {t("folders.checkDeletes")}
                                    </Button>
                                ) : f.phase === "paused" ? (
                                    <Button size="sm" icon="play" onClick={() => act("folders.resume", { name: f.name })}>
                                        {t("folders.resume")}
                                    </Button>
                                ) : (
                                    <Button size="sm" variant="ghost" icon="pause" onClick={() => act("folders.pause", { name: f.name })}>
                                        {t("folders.pause")}
                                    </Button>
                                )}
                                <Button size="sm" variant="ghost" icon="x" onClick={() => setRemoving(f)} />
                            </div>
                        </div>
                    ))}
                </div>
            </GlassCard>

            {adding && (
                <AddModal
                    initial={adding}
                    onClose={() => setAdding(null)}
                    onAdd={async (path, name) => (await act("folders.add", { path, name })) && setAdding(null)}
                />
            )}
            {reviewing && (
                <AcceptModal
                    folder={folders.find((f) => f.name === reviewing.name) ?? reviewing}
                    onClose={() => setReviewing(null)}
                    onAccept={async (prefer) => (await act("folders.accept", { name: reviewing.name, prefer })) && setReviewing(null)}
                />
            )}
            {removing && (
                <Modal
                    title={t("folders.removeTitle", { name: removing.name })}
                    onClose={() => setRemoving(null)}
                    footer={
                        <>
                            <Button variant="ghost" onClick={() => setRemoving(null)}>{t("common.cancel")}</Button>
                            <Button variant="danger" onClick={async () => (await act("folders.rm", { name: removing.name })) && setRemoving(null)}>
                                {t("folders.remove")}
                            </Button>
                        </>
                    }
                >
                    <p>{t("folders.removeBody")}</p>
                </Modal>
            )}
            {deletesFor && (
                <Modal
                    title={t("folders.deletesTitle", { name: deletesFor.name })}
                    onClose={() => setDeletesFor(null)}
                    footer={
                        <>
                            <Button variant="ghost" onClick={() => setDeletesFor(null)}>{t("common.cancel")}</Button>
                            <Button
                                variant="danger"
                                onClick={async () =>
                                    (await act("folders.resume", { name: deletesFor.name, accept_deletes: true })) && setDeletesFor(null)
                                }
                            >
                                {t("folders.acceptDeletes")}
                            </Button>
                        </>
                    }
                >
                    <p>{deletesFor.reason}</p>
                    <p className="dim">{t("folders.deletesBody")}</p>
                </Modal>
            )}
        </>
    );
}

function detail(f: FolderStatus, now: number, t: (k: string, v?: Record<string, string | number>) => string): string {
    if (f.phase === "preview" && f.preview) {
        return t("folders.previewShort", { in: f.preview.fetch, out: f.preview.send, conflicts: f.preview.conflicts?.length ?? 0 });
    }
    if (f.pending > 0) return t("folders.pending", { n: f.pending });
    if (f.phase === "idle" && f.last_sync) return t("folders.inSyncFor", { t: since(f.last_sync, now) });
    return f.reason ?? "";
}

function AddModal({ initial, onClose, onAdd }: { initial: { path: string; name: string }; onClose: () => void; onAdd: (path: string, name: string) => void }) {
    const { t } = useI18n();
    const [name, setName] = useState(initial.name);
    return (
        <Modal
            title={t("folders.addTitle")}
            onClose={onClose}
            footer={
                <>
                    <Button variant="ghost" onClick={onClose}>{t("common.cancel")}</Button>
                    <Button variant="primary" disabled={!name} onClick={() => onAdd(initial.path, name)}>
                        {t("folders.add")}
                    </Button>
                </>
            }
        >
            <p className="mono">{initial.path}</p>
            <label className="dim">{t("folders.nameLabel")}</label>
            <Input value={name} onChange={(e) => setName(e.target.value.toLowerCase())} autoFocus />
            <p className="faint">{t("folders.nameHint")}</p>
        </Modal>
    );
}

function AcceptModal({ folder, onClose, onAccept }: { folder: FolderStatus; onClose: () => void; onAccept: (prefer: Record<string, string>) => void }) {
    const { t } = useI18n();
    const pv = folder.preview;
    const divergent = folder.divergent ?? pv?.divergent ?? [];
    const [prefer, setPrefer] = useState<Record<string, string>>({});

    return (
        <Modal
            wide
            title={t("folders.reviewTitle", { name: folder.name })}
            onClose={onClose}
            footer={
                <>
                    <Button variant="ghost" onClick={onClose}>{t("common.cancel")}</Button>
                    <Button variant="primary" icon="check" onClick={() => onAccept(prefer)}>
                        {t("folders.accept")}
                    </Button>
                </>
            }
        >
            {pv ? (
                <dl className="kv">
                    <dt>{t("folders.comes")}</dt>
                    <dd>{pv.fetch} {t("folders.files")} ({size(pv.fetch_bytes)})</dd>
                    <dt>{t("folders.goes")}</dt>
                    <dd>{pv.send} {t("folders.files")} ({size(pv.send_bytes)})</dd>
                    <dt>{t("folders.deleted")}</dt>
                    <dd>{t("folders.nothing")}</dd>
                </dl>
            ) : (
                <p className="dim">{t("folders.noPreview")}</p>
            )}

            {pv?.conflicts && pv.conflicts.length > 0 && (
                <>
                    <strong>{t("folders.conflicts", { n: pv.conflicts.length })}</strong>
                    <p className="dim">{t("folders.conflictsHint")}</p>
                    <div className="mono dim" style={{ maxHeight: 140, overflow: "auto" }}>
                        {pv.conflicts.map((c) => (
                            <div key={c}>{c}</div>
                        ))}
                    </div>
                </>
            )}

            {divergent.length > 0 && (
                <>
                    <strong>{t("folders.divergent", { n: divergent.length })}</strong>
                    <p className="dim">{t("folders.divergentHint")}</p>
                    {divergent.map((repo) => (
                        <div key={repo} style={{ display: "flex", gap: 12, alignItems: "center" }}>
                            <span className="mono" style={{ flex: 1 }}>{repo}</span>
                            <Select value={prefer[repo] ?? ""} onChange={(e) => setPrefer({ ...prefer, [repo]: e.target.value })}>
                                <option value="">{t("folders.hold")}</option>
                                <option value="local">{t("folders.keepHere")}</option>
                                <option value="peer">{t("folders.takePeer")}</option>
                            </Select>
                        </div>
                    ))}
                </>
            )}
        </Modal>
    );
}
