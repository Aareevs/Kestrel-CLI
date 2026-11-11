defmodule Shop.MixProject do
  use Mix.Project

  def project do
    [
      app: :shop,
      version: "0.1.0",
      elixir: "~> 1.15",
      deps: deps()
    ]
  end

  defp deps do
    [
      {:phoenix, "~> 1.7.10"},
      {:phoenix_html, "~> 4.0"},
      {:ecto_sql, "~> 3.10"},
      {:postgrex, ">= 0.0.0"},
      {:redix, "~> 1.2"},
      {:bandit, "~> 1.2"}
    ]
  end
end
