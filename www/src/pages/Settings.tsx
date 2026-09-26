import { useEffect, useState } from "react";
import { Button, GlassCard, Input, Notice, Select, Toggle } from "../components/ui";
import { useI18n, type Locale } from "../i18n";
import { call, type SynkError } from "../services/control";
import type { Setting } from "../services/types";

const toggles = ["clipboard", "audio.playback", "audio.mic"];

// O microfone do peer está na config, mas a direção ainda não foi feita: o
// toggle aparece desligado para não prometer o que não existe.
const unbuilt = ["audio.mic"];

export function Settings() {
    const { t, locale, setLocale } = useI18n();
    const [settings, setSettings] = useState<Setting[] | null>(null);
    const [error, setError] = useState<SynkError | null>(null);
    const [port, setPort] = useState("");

    useEffect(() => {
        call<Setting[]>("config.ls")
            .then((s) => {
                setSettings(s);
                setPort(s.find((x) => x.key === "port")?.value ?? "");
            })
            .catch((e) => setError(e as SynkError));
    }, []);

    const set = async (key: string, value: string) => {
        setError(null);
        try {
            setSettings(await call<Setting[]>("config.set", { key, value }));
        } catch (e) {
            setError(e as SynkError);
        }
    };

    const value = (key: string) => settings?.find((s) => s.key === key)?.value ?? "";

    return (
        <>
            <div className="page-head">
                <div>
                    <h1>{t("nav.settings")}</h1>
                    <p className="dim">{t("settings.subtitle")}</p>
                </div>
            </div>
            <Notice error={error} />

            {settings && (
                <GlassCard>
                    <h2>{t("settings.link")}</h2>
                    <div className="rows">
                        <div className="row">
                            <div className="row__main">
                                <strong>{t("settings.key.peer")}</strong>
                                <p className="dim">{t("settings.peerHint")}</p>
                            </div>
                            <span className="mono">{value("peer")}</span>
                        </div>
                        <div className="row">
                            <div className="row__main">
                                <strong>{t("settings.key.edge")}</strong>
                                <p className="dim">{t("settings.edgeHint")}</p>
                            </div>
                            <Select value={value("edge").toLowerCase()} onChange={(e) => set("edge", e.target.value)}>
                                {["left", "right", "top", "bottom"].map((edge) => (
                                    <option key={edge} value={edge}>{t(`settings.edge.${edge}`)}</option>
                                ))}
                            </Select>
                        </div>
                        {toggles.map((key) => (
                            <div className="row" key={key}>
                                <div className="row__main">
                                    <strong>{t(`settings.key.${key}`)}</strong>
                                    <p className="dim">{t(`settings.help.${key}`)}</p>
                                </div>
                                <Toggle
                                    checked={!unbuilt.includes(key) && value(key) === "true"}
                                    disabled={unbuilt.includes(key)}
                                    onChange={(on) => set(key, on ? "on" : "off")}
                                    label={key}
                                />
                            </div>
                        ))}
                        <div className="row">
                            <div className="row__main">
                                <strong>{t("settings.key.port")}</strong>
                                <p className="dim">{t("settings.portHint")}</p>
                            </div>
                            <Input style={{ width: 100 }} value={port} onChange={(e) => setPort(e.target.value)} />
                            <Button size="sm" disabled={port === value("port")} onClick={() => set("port", port)}>
                                {t("common.save")}
                            </Button>
                        </div>
                    </div>
                </GlassCard>
            )}

            <GlassCard>
                <h2>{t("settings.app")}</h2>
                <div className="row">
                    <div className="row__main">
                        <strong>{t("settings.language")}</strong>
                    </div>
                    <Select value={locale} onChange={(e) => setLocale(e.target.value as Locale)}>
                        <option value="pt-br">Português</option>
                        <option value="en">English</option>
                    </Select>
                </div>
            </GlassCard>
        </>
    );
}
