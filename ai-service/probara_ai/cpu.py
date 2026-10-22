"""Pinned CPU preparation shared by held-out validation and production."""
import torch

CPU_FORMAT = {"encoder": "dynamic-int8-linear", "head": "float32", "backend": "qnnpack", "torch": "2.8.0"}


def prepare_cpu(model):
    if torch.__version__.split("+")[0] != CPU_FORMAT["torch"]:
        raise ValueError("CPU preparation requires the pinned PyTorch version")
    if "qnnpack" not in torch.backends.quantized.supported_engines:
        raise ValueError("This CPU does not support the validated quantization backend")
    model.float().cpu().eval()
    torch.backends.quantized.engine = "qnnpack"
    # Leave the typed transformer head in float32. Quantizing its Linear
    # modules breaks upstream attention's fast path and changes decision math.
    torch.ao.quantization.quantize_dynamic(model.encoder, {torch.nn.Linear}, dtype=torch.qint8, inplace=True)
    return model
