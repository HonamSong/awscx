"""모달 화면(설정 값 편집)."""
from textual.app import ComposeResult
from textual.containers import Vertical
from textual.screen import ModalScreen
from textual.widgets import Input, Label


class ConfigEditScreen(ModalScreen):
    CSS = """
    ConfigEditScreen { align: center middle; }
    #editbox {
        width: 70; height: auto; padding: 1 2;
        border: round $accent; background: $surface;
    }
    #edit-label { color: $accent; padding-bottom: 1; }
    #edit-hint { color: $text-muted; padding-top: 1; }
    """
    BINDINGS = [("escape", "cancel", "취소")]

    def __init__(self, label, value):
        super().__init__()
        self._label = label
        self._value = value

    def compose(self) -> ComposeResult:
        with Vertical(id="editbox"):
            yield Label(f"{self._label}", id="edit-label")
            yield Input(value=str(self._value), id="edit-input")
            yield Label("Enter=저장   Esc=취소", id="edit-hint")

    def on_mount(self):
        self.query_one("#edit-input", Input).focus()

    def on_input_submitted(self, event):
        self.dismiss(event.value)

    def action_cancel(self):
        self.dismiss(None)

