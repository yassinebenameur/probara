import hashlib
import sys
from pathlib import Path
import pytest

# probara_ai and the training helpers (compose_artifact, upstream) are imported
# as top-level modules, exactly as the scripts and the image run them.
ROOT = Path(__file__).resolve().parents[1]
for path in (ROOT, ROOT / "training"):
    if str(path) not in sys.path:
        sys.path.insert(0, str(path))


@pytest.fixture(scope="session")
def base(tmp_path_factory):
    """A tiny laya checkpoint and its lock, standing in for the pinned base."""
    torch = pytest.importorskip("torch")
    pytest.importorskip("laya")
    from tokenizers import Tokenizer, models, pre_tokenizers
    from transformers import ModernBertConfig, PreTrainedTokenizerFast
    from laya.common import build_model
    from laya.train import save_checkpoint
    root = tmp_path_factory.mktemp("base")
    ModernBertConfig(vocab_size=64, hidden_size=64, intermediate_size=96, num_hidden_layers=2, num_attention_heads=2,
                     pad_token_id=0, bos_token_id=2, eos_token_id=3, cls_token_id=2, sep_token_id=3,
                     max_position_embeddings=128).save_pretrained(root / "encoder")
    cfg = {"encoder": "fixture/tiny", "head_layers": 1, "max_len": 64, "head_max_len": 32,
           "act_costs": {"escalate": 0.5}, "model_name": "rl-agent"}
    model = build_model(cfg, encoder_dir=str(root / "encoder"))
    generator = torch.Generator().manual_seed(7)
    with torch.no_grad():
        for parameter in model.parameters():
            parameter.copy_(torch.randn(parameter.shape, generator=generator) * 0.02)
        model.temperature.fill_(1.0)
    vocab = {"<pad>": 0, "<unk>": 1, "<bos>": 2, "<eos>": 3, **{f"w{i}": i + 4 for i in range(60)}}
    raw = Tokenizer(models.WordLevel(vocab, unk_token="<unk>"))
    raw.pre_tokenizer = pre_tokenizers.Whitespace()
    tokenizer = PreTrainedTokenizerFast(tokenizer_object=raw, pad_token="<pad>", unk_token="<unk>",
                                        bos_token="<bos>", eos_token="<eos>")
    save_checkpoint(model, tokenizer, cfg, str(root))
    (root / "README.md").write_text("fixture base\n")
    names = ("README.md", "encoder/config.json", "model.safetensors", "rl_agent_config.json",
             "tokenizer/tokenizer.json", "tokenizer/tokenizer_config.json")
    lock = {"repo": "fixture/tiny", "revision": "0" * 40, "upstream_commit": "1" * 40,
            "files": {name: hashlib.sha256((root / name).read_bytes()).hexdigest() for name in names}}
    return root, lock
