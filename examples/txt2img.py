"""Generate an image from a text prompt with SD-Turbo.

Pipe this to `colabctl exec ID --accelerator T4 -o examples`. The
generated PNG streams back through display() and is saved on your machine.
"""

import re

import torch
from IPython.display import display

PROMPT = "A hyper-detailed, ultra-fluffy fantasy creature sitting in a glowing mossy forest hollow. The creature is a mix between a baby red panda and a tiny cloud, with oversized, sparkling cosmic violet eyes, floppy bunny ears, and a soft, pastel-pink and cream fur coat that looks impossibly plush. It is holding a tiny, glowing star-shaped wildflower in its small paws, looking up with an innocent, curious expression. Magical fireflies and soft, warm bokeh lights float in the background. Whimsical, cinematic lighting, 8k resolution, photorealistic fur texture, enchanted forest aesthetic, Pixar-meets-Studio-Ghibli style."

# Load once, then keep the pipeline in the kernel for subsequent runs.
if "pipe" not in globals():
    import subprocess
    import sys

    subprocess.run(
        [sys.executable, "-m", "pip", "install", "-q",
         "diffusers", "transformers", "accelerate", "safetensors"],
        check=True,
    )
    from diffusers import AutoPipelineForText2Image

    pipe = AutoPipelineForText2Image.from_pretrained(
        "stabilityai/sd-turbo",
        torch_dtype=torch.float16,
        variant="fp16",
    ).to("cuda")
    print("Model loaded.")

# SD-Turbo is a distilled model: one or two steps and no classifier-free guidance.
image = pipe(prompt=PROMPT, num_inference_steps=2, guidance_scale=0.0).images[0]

slug = re.sub(r"[^a-z0-9]+", "_", PROMPT.lower())[:40].strip("_") or "image"
display(image, metadata={"filename": f"{slug}.png"})
print("Generated:", PROMPT)
