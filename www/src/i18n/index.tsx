import { createContext, useContext, useState, type ReactNode } from "react";
import ptBr from "./pt-br.json";
import en from "./en.json";

export type Locale = "pt-br" | "en";

type Dictionary = typeof ptBr;

const dictionaries: Record<Locale, Dictionary> = {
    "pt-br": ptBr,
    en: en,
};

function resolve(dict: unknown, key: string): string | undefined {
    let node = dict;
    for (const part of key.split(".")) {
        if (node === null || typeof node !== "object") return undefined;
        node = (node as Record<string, unknown>)[part];
    }
    return typeof node === "string" ? node : undefined;
}

function detectLocale(): Locale {
    const saved = localStorage.getItem("locale");
    if (saved === "pt-br" || saved === "en") return saved;
    return navigator.language.toLowerCase().startsWith("pt") ? "pt-br" : "en";
}

// Vars substitui {nome} no texto: t("folders.added", { name: "codes" }).
type Vars = Record<string, string | number>;

interface I18nContextValue {
    locale: Locale;
    setLocale: (locale: Locale) => void;
    t: (key: string, vars?: Vars) => string;
}

const I18nContext = createContext<I18nContextValue | null>(null);

export function I18nProvider({ children }: { children: ReactNode }) {
    const [locale, setLocaleState] = useState<Locale>(detectLocale);

    const setLocale = (next: Locale) => {
        localStorage.setItem("locale", next);
        setLocaleState(next);
    };

    const t = (key: string, vars?: Vars): string => {
        const text = resolve(dictionaries[locale], key) ?? resolve(dictionaries.en, key) ?? key;
        if (!vars) return text;
        return text.replace(/\{(\w+)\}/g, (m, name) => (name in vars ? String(vars[name]) : m));
    };

    return <I18nContext.Provider value={{ locale, setLocale, t }}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18nContextValue {
    const ctx = useContext(I18nContext);
    if (!ctx) throw new Error("useI18n deve ser usado dentro de I18nProvider");
    return ctx;
}
