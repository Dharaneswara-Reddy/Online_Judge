import { LANGUAGE_META, LANGUAGE_ORDER } from "../../data/starterCode";
import "./LanguageSelector.css";

export default function LanguageSelector({ value, onChange, disabled }) {
  return (
    <div className="lang-select-wrap">
      <select
        className="lang-select"
        value={value}
        disabled={disabled}
        onChange={(e) => onChange(e.target.value)}
        aria-label="Select language"
      >
        {LANGUAGE_ORDER.map((id) => (
          <option key={id} value={id}>
            {LANGUAGE_META[id].label}
          </option>
        ))}
      </select>
    </div>
  );
}
