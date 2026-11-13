from pathlib import Path


def initialize_portable_data(bundle: Path) -> None:
    data = bundle / 'app-data'
    data.mkdir(exist_ok=False)
    (data / 'sources.json').write_bytes(b'{}\n')
