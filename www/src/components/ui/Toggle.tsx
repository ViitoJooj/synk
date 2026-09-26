import "./Toggle.scss";

export interface ToggleProps {
    checked: boolean;
    onChange: (checked: boolean) => void;
    disabled?: boolean;
    label?: string;
}

export function Toggle({ checked, onChange, disabled, label }: ToggleProps) {
    return (
        <button
            type="button"
            role="switch"
            aria-checked={checked}
            aria-label={label}
            disabled={disabled}
            className={`sk-toggle ${checked ? "sk-toggle--on" : ""}`}
            onClick={() => onChange(!checked)}
        >
            <span className="sk-toggle__thumb" />
        </button>
    );
}
