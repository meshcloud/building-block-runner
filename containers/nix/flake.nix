{
  description = "The nixpkgs that the tf image pins as the registry entry nixpkgs";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixpkgs-unstable";

  outputs = _: { };
}
