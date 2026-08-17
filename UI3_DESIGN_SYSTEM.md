# OmniBridge UI 3.0 Design System

UI 3.0 является новым presentation layer и не наследует компоновку alpha18.

## Основа

- fixed window: 1137×706, `NoResize`, custom title bar;
- sidebar: 190 px;
- app background: `#050912`;
- surfaces: `#0B1624`, `#0F1D2E`, `#14253A`;
- border: `#20354D`;
- primary: `#2584FF`;
- accent: `#7A3FF2`;
- success/warning/error: `#45DC6D` / `#E7C342` / `#FF6868`;
- radii: 8–14 px, hero 13–16 px;
- typography: Segoe UI, 9–28 px semantic scale.

## Presentation structure

- `ui/Theme.xaml` — colors, brushes, vector icons, typography, controls and card styles;
- `ui/Shell.xaml` — title bar, brand/sidebar, page host, lifecycle card, hint/action bar;
- `ui/pages/` — Dashboard, API Keys, Accounts, Models, Doctor, Integration, Logs, Settings, About;
- `scripts/desktop.ps1` — controller and presentation mapping without server logic.

Навигационные и служебные иконки являются vector geometry. Unicode-глифы не используются как иконки разделов.
