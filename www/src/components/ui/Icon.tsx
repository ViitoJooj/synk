// Ícones de traço, 24x24, desenhados à mão para não trazer uma biblioteca
// inteira por meia dúzia deles.
const paths = {
    link: "M10 14a4 4 0 0 0 5.66 0l3-3a4 4 0 0 0-5.66-5.66l-1 1M14 10a4 4 0 0 0-5.66 0l-3 3a4 4 0 0 0 5.66 5.66l1-1",
    monitor: "M3 5h18v11H3zM8 20h8M12 16v4",
    folder: "M3 7a2 2 0 0 1 2-2h4l2 2h8a2 2 0 0 1 2 2v8a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z",
    sliders: "M4 6h10M18 6h2M4 12h4M12 12h8M4 18h12M20 18h0M14 4v4M8 10v4M16 16v4",
    list: "M8 6h13M8 12h13M8 18h13M3 6h.01M3 12h.01M3 18h.01",
    plus: "M12 5v14M5 12h14",
    pause: "M8 5v14M16 5v14",
    play: "M7 5l12 7-12 7z",
    trash: "M4 7h16M9 7V4h6v3M6 7l1 13h10l1-13",
    check: "M5 12l5 5L19 7",
    x: "M6 6l12 12M18 6L6 18",
    shield: "M12 3l8 3v6c0 4.5-3.4 8.3-8 9-4.6-.7-8-4.5-8-9V6z",
    refresh: "M20 11a8 8 0 1 0-2.3 5.7M20 5v6h-6",
    restore: "M4 12a8 8 0 1 0 2.3-5.7M4 5v6h6",
    alert: "M12 8v5M12 17h.01M10.3 3.9L2.6 17.3A2 2 0 0 0 4.3 20h15.4a2 2 0 0 0 1.7-2.7L13.7 3.9a2 2 0 0 0-3.4 0z",
} as const;

export type IconName = keyof typeof paths;

export function Icon({ name, size = 16 }: { name: IconName; size?: number }) {
    return (
        <svg
            width={size}
            height={size}
            viewBox="0 0 24 24"
            fill="none"
            stroke="currentColor"
            strokeWidth={1.8}
            strokeLinecap="round"
            strokeLinejoin="round"
            aria-hidden
        >
            <path d={paths[name]} />
        </svg>
    );
}
